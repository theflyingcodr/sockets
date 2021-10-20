package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/theflyingcodr/sockets"
	"github.com/theflyingcodr/sockets/middleware"
)

type opts struct {
	writeTimeout    time.Duration
	pongWait        time.Duration
	pingPeriod      time.Duration
	maxMessageBytes int64
}

func defaultOpts() *opts {
	o := &opts{
		writeTimeout: 2 * time.Second,
		pongWait:     60 * time.Second,

		maxMessageBytes: 512,
	}
	o.pingPeriod = (o.pongWait * 9) / 10
	return o
}

// OptFunc defines a functional option to pass to the server at setup time.
type OptFunc func(c *opts)

// WithWriteTimeout defines the timeout length that the client will wait before
// failing the write.
// Default is 60 seconds.
func WithWriteTimeout(t time.Duration) OptFunc {
	return func(c *opts) {
		c.writeTimeout = t
	}
}

// WithPongTimeout defines the wait time the client will wait for a pong response
// from the server.
// Default is 60 seconds.
func WithPongTimeout(t time.Duration) OptFunc {
	return func(c *opts) {
		c.pongWait = t
	}
}

// WithPingPeriod will define the break between pings to the server.
// This should always be less than PongTimeout.
func WithPingPeriod(i time.Duration) OptFunc {
	return func(c *opts) {
		c.pingPeriod = i
	}
}

// WithMaxMessageSize defines the maximum message size in bytes that
// the client will accept.
// Default is 512 bytes.
func WithMaxMessageSize(s int64) OptFunc {
	return func(c *opts) {
		c.maxMessageBytes = s
	}
}

// SocketServer is a central point that connects peers together.
// It manages connections and channels as well as sending of messages
// to peers.
//
// It can have listeners setup both for channel broadcast and direct broadcast.
type SocketServer struct {
	// maps clientID to roomID for direct client messaging
	clientConnections  map[string]*connection
	channels           map[string]*channel
	broadcastListeners map[string]sockets.HandlerFunc
	directListeners    map[string]sockets.HandlerFunc
	middleware         []sockets.MiddlewareFunc
	errHandler         sockets.ServerErrorHandlerFunc
	channelCloser      chan string
	unregister         chan unregister
	register           chan register
	channelSender      chan sender
	directSender       chan sender
	errs               chan *sockets.ErrorMessage
	close              chan struct{}
	done               chan struct{}
	i                  *info
	opts               *opts
	sync.RWMutex
}

// NewSocketServer will setup and return a new instance of a SocketServer.
func NewSocketServer(opts ...OptFunc) *SocketServer {
	defaults := defaultOpts()

	for _, o := range opts {
		o(defaults)
	}

	s := &SocketServer{
		clientConnections:  make(map[string]*connection),
		channels:           make(map[string]*channel),
		broadcastListeners: make(map[string]sockets.HandlerFunc),
		directListeners:    make(map[string]sockets.HandlerFunc),
		middleware:         make([]sockets.MiddlewareFunc, 0),
		errHandler:         defaultErrorHandler,
		channelCloser:      make(chan string),
		unregister:         make(chan unregister, 1),
		register:           make(chan register, 1),
		close:              make(chan struct{}, 1),
		done:               make(chan struct{}, 1),
		channelSender:      make(chan sender, 256),
		directSender:       make(chan sender, 256),
		errs:               make(chan *sockets.ErrorMessage, 256),
		opts:               defaults,
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
				_ = conn.ws.Close()
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
				ch = newChannel(u.channelID)
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
				log.Debug().Msgf("channel %s is nil", m.ID)
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
					log.Debug().Msgf("channel %s is nil", m.ID)
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

// Listen will start up a new listener for the received connection and channelID.
//
// This would be called after an Upgrade call in an http handler usually in a go routine.
func (s *SocketServer) Listen(conn *websocket.Conn, channelID string) error {
	if channelID == "" {
		return errors.New("channelID cannot be empty")
	}
	conn.SetReadLimit(s.opts.maxMessageBytes)
	_ = conn.SetReadDeadline(time.Now().Add(s.opts.pongWait))
	conn.SetPongHandler(func(string) error { _ = conn.SetReadDeadline(time.Now().Add(s.opts.pongWait)); return nil })

	clientID := uuid.NewString()
	log.Info().Msgf("receiving new connection with clientID %s", clientID)
	c := &connection{
		ws:       conn,
		send:     make(chan interface{}, 1),
		clientID: clientID,
		opts:     s.opts,
	}
	go c.writer()
	log.Debug().Msgf("adding connection to channelID %s", channelID)
	s.register <- register{
		channelID:  channelID,
		clientID:   clientID,
		connection: c,
	}
	log.Debug().Msgf("client %s added to channelID %s", clientID, channelID)
	defer func() {
		s.unregisterClient(channelID, clientID)
		log.Debug().Msgf("removed clientID %s", clientID)
	}()
	s.BroadcastDirect(clientID, sockets.NewMessage(sockets.MessageJoinSuccess, clientID, channelID))

	log.Info().Msgf("connection with clientID %s added, listening for messages", clientID)

	for {
		var m *sockets.Message
		if err := conn.ReadJSON(&m); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.Error().Msgf("error: %v", err)
			}
			break
		}
		m.ClientID = clientID
		ctx := context.Background()
		log.Debug().Msg("message received")
		hndlr, isDirect := s.handler(m.Key())
		if hndlr == nil {
			log.Debug().Msgf("no handler found for message %s", m.Key())
			continue
		}
		log.Debug().Msgf("executing handler for message %s", m.Key())
		resp, err := middleware.ExecMiddlewareChain(hndlr, s.middleware)(ctx, m)
		if err != nil {
			errMsg := s.errHandler(*m, err)
			if errMsg == nil {
				continue
			}
			s.directSender <- sender{
				ID:  clientID,
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
				ID:  resp.ClientID,
				msg: resp,
			}
			continue
		}
		log.Debug().Msgf("sending channel message")
		s.channelSender <- sender{
			ID:  resp.ChannelID(),
			msg: resp,
		}
		log.Debug().Msgf("channel message sent")
	}
	return nil
}

// WithErrorHandler can be used to overwrite the default error handler.
func (s *SocketServer) WithErrorHandler(fn sockets.ServerErrorHandlerFunc) *SocketServer {
	s.errHandler = fn
	return s
}

// WithInfo will enable the info listener which responds with information on the server.
func (s *SocketServer) WithInfo() *SocketServer {
	s.RegisterDirectHandler(sockets.MessageGetInfo, s.infoListener)
	return s
}

// handler will return a handler, checking for direct listeners first, if not found nil is returned.
func (s *SocketServer) handler(name string) (sockets.HandlerFunc, bool) {
	s.RLock()
	defer s.RUnlock()
	l, ok := s.directListeners[name]
	if ok {
		return l, true
	}
	return s.broadcastListeners[name], false
}

// Close should always be called in a defer to allow the server
// to gracefully shutdown and close underling connections.
func (s *SocketServer) Close() {
	s.close <- struct{}{}
	<-s.done
}

// Broadcast will send a message to a channel.
//
// This is used if a server event happens that needs to be sent to all clients
// without a message being sent first via a listener.
func (s *SocketServer) Broadcast(channelID string, msg *sockets.Message) {
	s.channelSender <- sender{
		ID:  channelID,
		msg: msg,
	}
}

// BroadcastDirect will send a message directly to a client.
//
// This is used if a server event happens that needs to be sent to a client
// without a message being sent first via a listener.
func (s *SocketServer) BroadcastDirect(clientID string, msg *sockets.Message) {
	s.directSender <- sender{
		ID:  clientID,
		msg: msg,
	}
}

// WithMiddleware will append the middleware funcs to any already registered middleware functions.
// When adding middleware, it is recommended to always add a PanicHandler first as this will ensure your
// application has the best chance of recovering. There is a default panic handler available under sockets.PanicHandler.
func (s *SocketServer) WithMiddleware(mws ...sockets.MiddlewareFunc) *SocketServer {
	s.middleware = append(s.middleware, mws...)
	return s
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
