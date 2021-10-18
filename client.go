package sockets

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 2 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

type clientOpts struct {
	reconnect         bool
	reconnectAttempts int
	reconnectTimeout  time.Duration
}

func defaultOpts() *clientOpts {
	return &clientOpts{
		reconnect:         false,
		reconnectAttempts: 3,
		reconnectTimeout:  30 * time.Second,
	}
}

type ClientOptFunc func(c *clientOpts)

// WithReconnect will enable reconnects from a client,
// in the event of a connection loss with a server the client
// will attempt to reconnect.
//
// Default values are to retry 3 times with a 30 second wait between retry.
func WithReconnect() ClientOptFunc {
	return func(c *clientOpts) {
		c.reconnect = true
	}
}

// WithReconnectAttempts will overwrite the default connection attempts of
// 3 with value attempts, when this value is exceeded the connection will
// cease to re-connect and exit.
func WithReconnectAttempts(attempts int) ClientOptFunc {
	return func(c *clientOpts) {
		c.reconnectAttempts = attempts
	}
}

// WithReconnectTimeout will overwrite the default timeout between reconnect
// attempts of 30 seconds with value t.
func WithReconnectTimeout(t time.Duration) ClientOptFunc {
	return func(c *clientOpts) {
		c.reconnectTimeout = t
	}
}

// WithInfiniteReconnect will make the client listen forever for the server to
// reconnect in the event of a connection loss.
func WithInfiniteReconnect() ClientOptFunc {
	return func(c *clientOpts) {
		c.reconnectAttempts = -1
	}
}

type Client struct {
	conn         map[string]*clientConnection
	listeners    map[string]HandlerFunc
	middleware   []MiddlewareFunc
	errHandler   ClientErrorHandler
	close        chan struct{}
	done         chan struct{}
	sender       chan sendMsg
	channelJoin  chan *clientConnection
	channelLeave chan string
	join         chan joinSuccess
	opts         *clientOpts
	sync.RWMutex
}

type joinSuccess struct {
	ChannelID string
	ClientID  string
}

// NewClient will setup a new websocket client which will connect to the server
// at the provided uri.
func NewClient(opts ...ClientOptFunc) *Client {
	o := defaultOpts()
	for _, opt := range opts {
		opt(o)
	}

	cli := &Client{
		conn:         make(map[string]*clientConnection),
		listeners:    make(map[string]HandlerFunc),
		middleware:   make([]MiddlewareFunc, 0),
		errHandler:   defaultClientErrorHandler,
		close:        make(chan struct{}, 1),
		done:         make(chan struct{}, 1),
		sender:       make(chan sendMsg, 256),
		channelJoin:  make(chan *clientConnection, 1),
		channelLeave: make(chan string, 1),
		join:         make(chan joinSuccess, 1),
		RWMutex:      sync.RWMutex{},
		opts:         o,
	}
	cli.RegisterListener(MessageJoinSuccess, cli.joinSuccess)
	go cli.connectionManager()
	return cli
}

// WithJoinRoomSuccessListener will replace the default room join success handler
// with a custom one. This will allow the implementor to define their own logic
// after a room is joined.
func (c *Client) WithJoinRoomSuccessListener(l HandlerFunc) *Client {
	c.Lock()
	defer c.Unlock()
	c.listeners[MessageJoinSuccess] = l
	return c
}

// WithMiddleware will append the middleware funcs to any already registered middleware functions.
// When adding middleware, it is recommended to always add a PanicHandler first as this will ensure your
// application has the best chance of recovering. There is a default panic handler available under sockets.PanicHandler.
func (c *Client) WithMiddleware(mws ...MiddlewareFunc) *Client {
	for _, mw := range mws {
		c.middleware = append(c.middleware, mw)
	}
	return c
}

// WithJoinRoomFailedListener will replace the default room join failed handler
// with a custom one. This will allow the implementor to define their own logic
// after a room is joined.
func (c *Client) WithJoinRoomFailedListener(l HandlerFunc) *Client {
	c.Lock()
	defer c.Unlock()
	c.listeners[MessageLeaveSuccess] = l
	return c
}

func (c *Client) listener(name string) HandlerFunc {
	c.RLock()
	defer c.RUnlock()
	return c.listeners[name]
}

// WithErrorHandler allows a user to overwrite the default error handler.
func (c *Client) WithErrorHandler(e ClientErrorHandler) *Client {
	c.Lock()
	defer c.Unlock()
	c.errHandler = e
	return c
}

// Close will ensure the client is gracefully shut down.
func (c *Client) Close() {
	log.Info().Msg("closing socket client")
	for _, conn := range c.conn {
		conn.close()
	}
	log.Info().Msg("socket client closed")
}

// websocket will send a join room message.
//
// If you need to authenticate with the server or send meta, add header/s.
func (c *Client) JoinChannel(host, channelID string, headers http.Header) error {
	log.Info().Msgf("joining channel %s", channelID)
	cl, err := newClientConnection(fmt.Sprintf("%s/%s", host, channelID), channelID, headers, c.channelLeave, c.opts, c.listeners, c.errHandler)
	if err != nil {
		return err
	}
	go cl.writer()
	c.channelJoin <- cl
	log.Info().Msgf("connected to channel %s", channelID)
	return nil
}

// LeaveRoom will remove a client from a room.
func (c *Client) LeaveChannel(channelID string, headers http.Header) {
	c.channelLeave <- channelID
}

func (c *Client) connectionManager() {
	for {
		select {
		case msg := <-c.sender:
			ch := c.conn[msg.m.channelID]
			if ch == nil {
				continue
			}
			if ch.closing {
				c.channelLeave <- ch.channelID
				continue
			}
			msg.m.clientID = ch.clientID
			ch.send <- msg
		case join := <-c.channelJoin:
			c.conn[join.channelID] = join
		case channelID := <-c.channelLeave:
			fmt.Println("leaving channel")
			ch := c.conn[channelID]
			if ch == nil {
				continue
			}
			delete(c.conn, channelID)
			ch.close()
		case s := <-c.join:
			ch := c.conn[s.ChannelID]
			if ch == nil {
				continue
			}
			ch.clientID = s.ClientID
		}
	}
}

// Publish will broadcast a message to the server and wait for an error.
func (c *Client) Publish(channelID, msgType string, body interface{}, headers http.Header) error {
	if channelID == "" || msgType == "" {
		return errors.New("channelID and msgType required")
	}
	msg := NewMessage(msgType, "", channelID)
	if err := msg.WithBody(body); err != nil {
		return err
	}
	msg.Headers = headers
	err := make(chan error)
	defer close(err)
	c.sender <- sendMsg{
		m:      msg,
		notify: err,
	}
	return <-err
}

// RegisterListener will add a new listener to the client.
func (c *Client) RegisterListener(msgType string, fn HandlerFunc) *Client {
	c.Lock()
	defer c.Unlock()
	c.listeners[msgType] = fn
	return c
}
