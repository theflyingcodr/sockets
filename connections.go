package sockets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

type connection struct {
	ws       *websocket.Conn
	send     chan interface{}
	clientID string
}

// writer sends messages from the server to the websocket connection.
func (c *connection) writer() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.ws.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.write(websocket.CloseMessage, []byte{})
				log.Debug().Msgf("closing connection for clientID %s", c.clientID)
				return
			}
			if err := c.writeJSON(msg); err != nil {
				log.Err(err)
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, []byte{}); err != nil {
				log.Err(err)
				return
			}
		}
	}
}

// write writes a message with the given message type and payload.
func (c *connection) write(mt int, payload []byte) error {
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteMessage(mt, payload)
}

// write writes a message with the given message type and payload.
func (c *connection) writeJSON(payload interface{}) error {
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteJSON(payload)
}

type sendMsg struct {
	m      *Message
	notify chan error
}

type clientConnection struct {
	url             string
	ws              *websocket.Conn
	listeners       map[string]HandlerFunc
	mw              []MiddlewareFunc
	errHandler      ClientErrorHandler
	send            chan sendMsg
	done            chan struct{}
	closing         bool
	channelID       string
	clientID        string
	opts            *clientOpts
	reconnect       chan struct{}
	reconnectStatus chan bool
	leaveSignal     chan<- string
	sync.RWMutex
}

func newClientConnection(url, channelID string, h http.Header, leaveSignal chan<- string, opts *clientOpts, l map[string]HandlerFunc, errFn ClientErrorHandler) (*clientConnection, error) {
	ws, _, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		return nil, err
	}
	return &clientConnection{
		url:             url,
		ws:              ws,
		listeners:       l,
		errHandler:      errFn,
		send:            make(chan sendMsg, 1),
		done:            make(chan struct{}, 1),
		channelID:       channelID,
		opts:            opts,
		reconnect:       make(chan struct{}, 1),
		reconnectStatus: make(chan bool, 1),
		leaveSignal:     leaveSignal,
		RWMutex:         sync.RWMutex{},
	}, nil
}

// writer sends messages from the client to the websocket connection.
func (c *clientConnection) writer() {
	defer func() {
		c.ws.Close()
	}()
	go func() {
		defer close(c.done)
		for {
			var body map[string]interface{}
			msgType, bb, err := c.ws.ReadMessage()
			if msgType == websocket.CloseMessage {
				log.Info().
					Msgf("close message received for channelID '%s', closing connection", c.channelID)
				return
			}
			if err != nil {
				log.Err(err).Msg("error when reading message")
				//websocket.IsCloseError(err)
				if msgType == -1 {
					log.Info().Msg("lost connection to server, retrying")
					c.reconnect <- struct{}{}
					ok := <-c.reconnectStatus
					if !ok {
						log.Error().
							Msgf("failed to re-connect to %s after %d attempts, exiting client", c.url, c.opts.reconnectAttempts)
						return
					}
					fmt.Println("reconnect ok")
					continue
				}
				continue
			}
			if err := json.Unmarshal(bb, &body); err != nil {
				log.Err(err).Msg("error when reading message")
				continue
			}
			if body["type"] == MessageError {
				var errMsg *ErrorMessage
				if err := json.Unmarshal(bb, &errMsg); err != nil {
					continue
				}
				c.errHandler(errMsg)
				continue
			}
			var msg *Message
			if err := json.Unmarshal(bb, &msg); err != nil {
				log.Error().Err(err).Msg("unknown message type received")
				continue
			}
			ctx := context.Background()
			log.Debug().
				Str("channelID", msg.channelID).
				Str("clientID", msg.clientID).
				Str("type", msg.key).
				Msg("new message received")
			fn := c.listener(msg.key)
			if fn == nil {
				log.Info().Msgf("no handler found for message type '%s'", msg.key)
				continue
			}
			// exec middleware and then handler.
			resp, err := execMiddlewareChain(fn, c.mw)(ctx, msg)
			if err != nil {
				c.errHandler(resp.ToError(nil))
				continue
			}
			if resp != nil {
				c.send <- sendMsg{
					m:      resp,
					notify: nil,
				}
			}
		}
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.write(websocket.CloseMessage, []byte{})
				log.Debug().Msgf("closing connection for channelID %s", c.channelID)
				return
			}
			if err := c.writeJSON(msg.m); err != nil {
				if msg.notify != nil {
					msg.notify <- err
				}
				log.Err(err).Msgf("failed to write message")
				continue
			}
			if msg.notify != nil {
				msg.notify <- nil
			}
		case <-c.done:
			c.Lock()
			c.closing = true
			c.Unlock()
			return
		case <-c.reconnect:
			i := 0
			for {
				fmt.Println("hit")
				i++
				time.Sleep(c.opts.reconnectTimeout)
				ws, _, err := websocket.DefaultDialer.Dial(c.url, nil)
				if err != nil {
					log.Err(err).Msgf("failed to reconnect to '%s' after '%d' attempts", c.ws.UnderlyingConn().RemoteAddr(), i)
					if c.opts.reconnectAttempts != -1 && i > c.opts.reconnectAttempts {
						c.reconnectStatus <- false
						break
					}
					continue
				}
				c.ws = ws
				c.reconnectStatus <- true
				break
			}
		}
	}
}

func (c *clientConnection) isClosed() bool {
	c.RLock()
	defer c.RUnlock()
	return c.closing
}

func (c *clientConnection) listener(key string) HandlerFunc {
	c.RLock()
	defer c.RUnlock()
	return c.listeners[key]
}

// write writes a message with the given message type and payload.
func (c *clientConnection) write(mt int, payload []byte) error {
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteMessage(mt, payload)
}

// write writes a message with the given message type and payload.
func (c *clientConnection) writeJSON(payload *Message) error {
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteJSON(payload)
}

func (c *clientConnection) close() {
	if c.closing {
		return
	}
	c.done <- struct{}{}
}
