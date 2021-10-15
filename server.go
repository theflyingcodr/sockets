package sockets

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	MessageJoinSuccess  = "join.success"
	MessageLeaveSuccess = "leave.success"
	MessageGetInfo      = "get.info"
	MessageInfo         = "info"
	MessageError        = "error"
)

type SocketServer struct {
	// maps clientID to roomID for direct client messaging
	clientConnections  map[string]*connection
	channels           map[string]*Channel
	broadcastListeners map[string]HandlerFunc
	directListeners    map[string]HandlerFunc
	errHandler         ErrorHandlerFunc
	channelCloser      chan string
	unregister         chan unregister
	register           chan register
	channelSender      chan sender
	directSender       chan sender
	errs               chan *ErrorMessage
	close              chan struct{}
	done               chan struct{}
	i                  *info
	sync.RWMutex
}

func NewSocketServer() *SocketServer {
	s := &SocketServer{
		clientConnections:  make(map[string]*connection, 0),
		channels:           make(map[string]*Channel, 0),
		broadcastListeners: make(map[string]HandlerFunc, 0),
		directListeners:    make(map[string]HandlerFunc, 0),
		errHandler:         defaultServerErrorHandler,
		channelCloser:      make(chan string),
		unregister:         make(chan unregister, 1),
		register:           make(chan register, 1),
		close:              make(chan struct{}, 1),
		done:               make(chan struct{}, 1),
		channelSender:      make(chan sender, 256),
		directSender:       make(chan sender, 256),
		errs:               make(chan *ErrorMessage, 256),
	}
	go s.channelManager()
	return s
}

func (s *SocketServer) channelManager() {
	for {
		select {
		case <-s.close:
			close(s.channelSender)
			close(s.directSender)
			log.Info().Msg("closing server")
			for _, c := range s.clientConnections {
				_ = c.ws.Close()
			}
			close(s.unregister)
			close(s.register)
			close(s.channelCloser)

			log.Info().Msg("connections terminated")
			s.done <- struct{}{}
			return
		case channelID := <-s.channelCloser:
			ch := s.channels[channelID]
			delete(s.channels, channelID)
			if ch == nil {
				continue
			}
			s.updateInfo(&info{
				totalConnections: len(s.clientConnections),
				totalChannels:    len(s.channels),
			})
		case u := <-s.unregister:
			conn, ok := s.clientConnections[u.clientID]
			if ok && conn != nil {
				conn.ws.Close()
			}
			delete(s.clientConnections, u.clientID)
			ch := s.channels[u.channelID]
			if ch == nil {
				continue
			}
			delete(ch.conns, u.clientID)
			if len(ch.conns) == 0 {
				delete(s.channels, u.channelID)
			}
			s.updateInfo(&info{
				totalConnections: len(s.clientConnections),
				totalChannels:    len(s.channels),
			})
		case u := <-s.register:
			s.clientConnections[u.clientID] = u.connection
			ch, ok := s.channels[u.channelID]
			if !ok {
				ch = NewChannel(u.channelID)
				s.channels[u.channelID] = ch
			}
			ch.conns[u.clientID] = u.connection
			s.updateInfo(&info{
				totalConnections: len(s.clientConnections),
				totalChannels:    len(s.channels),
			})
		case m := <-s.channelSender:
			ch := s.channels[m.ID]
			if ch == nil {
				log.Debug().Msgf("channel %s is nil")
				continue
			}
			for _, sub := range ch.conns {
				sub.send <- m.msg
			}
			// clear buffer
			n := len(s.channelSender)
			for i := 0; i < n; i++ {
				ch := s.channels[m.ID]
				if ch == nil {
					log.Debug().Msgf("channel %s is nil")
					continue
				}
				for _, sub := range ch.conns {
					sub.send <- m.msg
				}
			}
		case m := <-s.directSender:
			ch := s.clientConnections[m.ID]
			if ch == nil {
				continue
			}
			ch.send <- m.msg
			// clear buffer
			n := len(s.directSender)
			for i := 0; i < n; i++ {
				ch := s.clientConnections[m.ID]
				if ch == nil {
					continue
				}
				ch.send <- m.msg
			}
		}
	}
}

// ListenRoom will start up a new listener for the received connection and roomID.
func (s *SocketServer) Listen(conn *websocket.Conn, channelID string) error {
	if channelID == "" {
		return errors.New("channelID cannot be empty")
	}
	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	clientId := uuid.NewString()
	log.Info().Msgf("receiving new connection with clientID %s", clientId)
	c := &connection{
		ws:       conn,
		send:     make(chan interface{}, 1),
		clientID: clientId,
	}
	go c.writer()
	log.Debug().Msgf("adding connection to channelID %s", channelID)
	s.register <- register{
		channelID:  channelID,
		clientID:   clientId,
		connection: c,
	}
	log.Debug().Msgf("client %s added to channelID %s", clientId, channelID)
	defer func() {
		s.unregisterClient(channelID, clientId)
		log.Debug().Msgf("removed clientID %s", clientId)
	}()
	s.BroadcastDirect(clientId, NewMessage(MessageJoinSuccess, clientId, channelID))

	log.Info().Msgf("connection with clientID %s added, listening for messages", clientId)

	for {
		var m *Message
		if err := conn.ReadJSON(&m); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.Error().Msgf("error: %v", err)
			}
			break
		}
		m.clientID = clientId
		ctx := context.Background()
		log.Debug().Msg("message received")
		hndlr, isDirect := s.handler(m.key)
		if hndlr == nil {
			log.Debug().Msgf("no handler found for message %s", m.key)
			continue
		}
		log.Debug().Msgf("executing handler for message %s", m.key)
		resp, err := hndlr(ctx, m)
		if err != nil {
			errMsg := s.errHandler(*m, err)
			if errMsg == nil {
				continue
			}
			s.directSender <- sender{
				ID:  clientId,
				msg: errMsg,
			}
			continue
		}
		// no response, nothing to broadcast
		if resp == nil {
			log.Debug().Msgf("nothing to broadcast")
			continue
		}
		if isDirect {
			log.Debug().Msgf("sending direct message")
			s.directSender <- sender{
				ID:  resp.clientID,
				msg: resp,
			}
			continue
		}
		log.Debug().Msgf("sending channel message")
		s.channelSender <- sender{
			ID:  resp.channelID,
			msg: resp,
		}
		log.Debug().Msgf("channel message sent")
	}
	return nil
}

// WithErrorHandler can be used to overwrite the default error handler.
func (s *SocketServer) WithErrorHandler(fn ErrorHandlerFunc) *SocketServer {
	s.errHandler = fn
	return s
}

// WithInfo will enable the info listener which responds with information on the server.
func (s *SocketServer) WithInfo() *SocketServer {
	s.RegisterDirectHandler(MessageGetInfo, s.infoListener)
	return s
}

// handler will return a handler, checking for direct listeners first, if not found nil is returned.
func (s *SocketServer) handler(name string) (HandlerFunc, bool) {
	s.RLock()
	defer s.RUnlock()
	l, ok := s.directListeners[name]
	if ok {
		return l, true
	}
	return s.broadcastListeners[name], false
}

func (s *SocketServer) Close() {
	s.close <- struct{}{}
	<-s.done
}

// Broadcast will send a message to a channel.
//
// This is used if a server event happens that needs to be sent to all clients
// without a message being sent first via a listener.
func (s *SocketServer) Broadcast(channelID string, msg *Message) {
	s.channelSender <- sender{
		ID:  channelID,
		msg: msg,
	}
}

// BroadcastDirect will send a message directly to a client.
//
// This is used if a server event happens that needs to be sent to a client
// without a message being sent first via a listener.
func (s *SocketServer) BroadcastDirect(clientID string, msg *Message) {
	s.directSender <- sender{
		ID:  clientID,
		msg: msg,
	}
}

func (s *SocketServer) info() *info {
	s.RLock()
	defer s.RUnlock()
	return s.i
}

func (s *SocketServer) updateInfo(i *info) {
	s.Lock()
	defer s.Unlock()
	s.i = i
}

func (s *SocketServer) unregisterClient(channelID, clientID string) {
	s.unregister <- unregister{channelID, clientID}
}

type register struct {
	channelID  string
	clientID   string
	connection *connection
}

type unregister struct {
	channelID string
	clientID  string
}

type sender struct {
	ID  string
	msg interface{}
}

type info struct {
	totalConnections int
	totalChannels    int
}
