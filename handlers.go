package sockets

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

type HandlerFunc func(ctx context.Context, msg *Message) (*Message, error)

type ErrorHandlerFunc func(msg Message, e error) *ErrorMessage

// ErrorDetail is returned as the message body in the event of an error.
type ErrorDetail struct {
	Title       string
	Description string
	ErrCode     string
}

// defaultServerErrorHandler will simply log the error and then add some details
// to the message body before returning to the client the message was sent from.
func defaultServerErrorHandler(msg Message, e error) *ErrorMessage {
	log.Error().
		Str("id", msg.id).
		Str("trace", fmt.Sprintf("%v", e)).
		Str("msgType", msg.key).
		Err(e)

	return msg.ToError(ErrorDetail{
		Title:       "unexpected server error",
		Description: e.Error(),
		ErrCode:     "500",
	})
}

type ClientErrorHandler func(msg *ErrorMessage)

func defaultClientErrorHandler(msg *ErrorMessage) {
	var err ErrorDetail
	_ = msg.Bind(&err)
	log.Error().Str("originKey", msg.Key).
		Str("correlationID", msg.CorrelationID).
		RawJSON("errorDetail", msg.ErrorBody).
		Str("channelID", msg.ChannelID).Msg("server error received")
}

// RegisterDirectHandler will register handlers that respond ONLY to the client
// that sent them a message, no other clients will receive the notification.
func (s *SocketServer) RegisterDirectHandler(key string, fn HandlerFunc) *SocketServer {
	s.Lock()
	defer s.Unlock()
	s.directListeners[key] = fn
	return s
}

// RegisterChannelHandler will add a handler that when sending a message will send to ALL clients
// connected to the channelID in the message.
func (s *SocketServer) RegisterChannelHandler(name string, fn HandlerFunc) *SocketServer {
	s.Lock()
	defer s.Unlock()
	s.broadcastListeners[name] = fn
	return s
}

// Info will return information on the current server.
func (s *SocketServer) infoListener(ctx context.Context, msg *Message) (*Message, error) {
	inf := s.info()
	i := struct {
		TotalConnections int `json:"totalConnections"`
		TotalChannels    int `json:"totalChannels"`
	}{
		TotalChannels:    inf.totalChannels,
		TotalConnections: inf.totalConnections,
	}
	resp := msg.NewFrom(MessageInfo)
	if err := resp.WithBody(i); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) joinSuccess(ctx context.Context, msg *Message) (*Message, error) {
	log.Debug().Msgf("joined channel %s success", msg.channelID)
	c.join <- joinSuccess{
		ChannelID: msg.channelID,
		ClientID:  msg.clientID,
	}
	return msg.NoContent()
}
