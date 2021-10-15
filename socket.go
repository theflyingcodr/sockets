package sockets

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type Message struct {
	CorrelationID string
	AppID         string
	UserID        string
	Expiration    *time.Time
	Body          json.RawMessage
	id            string
	channelID     string
	timestamp     time.Time
	key           string
	Headers       http.Header
	clientID      string
}

type messageJSON struct {
	CorrelationID string          `json:"correlationId"`
	AppID         string          `json:"appId"`
	ClientID      string          `json:"clientID"`
	UserID        string          `json:"userId"`
	Expiration    *time.Time      `json:"expiration"`
	Body          json.RawMessage `json:"body"`
	ID            string          `json:"messageId"`
	ChannelID     string          `json:"channelId"`
	Timestamp     time.Time       `json:"timestamp"`
	Key           string          `json:"type"`
	Headers       http.Header     `json:"headers"`
}

func (m *Message) MarshalJSON() ([]byte, error) {
	return json.Marshal(messageJSON{
		CorrelationID: m.CorrelationID,
		AppID:         m.AppID,
		ClientID:      m.clientID,
		UserID:        m.UserID,
		Expiration:    m.Expiration,
		Body:          m.Body,
		ID:            m.id,
		ChannelID:     m.channelID,
		Timestamp:     m.timestamp,
		Key:           m.key,
		Headers:       m.Headers,
	})
}

func (m *Message) UnmarshalJSON(bb []byte) error {
	var j *messageJSON
	if err := json.Unmarshal(bb, &j); err != nil {
		return err
	}

	m.CorrelationID = j.CorrelationID
	m.AppID = j.AppID
	m.UserID = j.UserID
	m.Expiration = j.Expiration
	m.Body = j.Body
	m.id = j.ID
	m.channelID = j.ChannelID
	m.timestamp = j.Timestamp
	m.key = j.Key
	m.Headers = j.Headers
	m.clientID = j.ClientID

	return nil
}

func (m *Message) ID() string {
	return m.id
}

func (m *Message) Timestamp() time.Time {
	return m.timestamp
}

func (m *Message) ChannelID() string {
	return m.channelID
}

// NewFrom will take a copy of msg and return a new message from it.
//
// You can then add a body using the WithBody func and add headers etc.
func (m *Message) NewFrom(key string) *Message {
	msg := NewMessage(key, m.clientID, m.channelID)
	msg.Expiration = m.Expiration
	msg.UserID = m.UserID
	msg.AppID = m.AppID
	msg.CorrelationID = m.CorrelationID
	return msg
}

// NewMessage will create a new message setting
func NewMessage(msgType, clientID, channelID string) *Message {
	return &Message{
		id:        uuid.NewString(),
		timestamp: time.Now().UTC(),
		key:       msgType,
		Headers:   http.Header{},
		channelID: channelID,
		clientID:  clientID,
	}
}

// Bind will map the body to v.
func (m Message) Bind(v interface{}) error {
	if m.Body == nil {
		return nil
	}
	// TODO - handle different binding ie params, headers xml etc
	return json.Unmarshal(m.Body, &v)
}

// WithBody will serialise the value v into the message body.
func (m *Message) WithBody(v interface{}) error {
	bb, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.Body = bb
	return nil
}

// NoContent is a helper that can be used to return an empty message from a listener.
func (m *Message) NoContent() (*Message, error) {
	return nil, nil
}

// DirectBroadcaster is used to send a message directly to a client.
type DirectBroadcaster interface {
	BroadcastDirect(clientID string, msg *Message)
}

// ChannelBroadcaster is used to send a message to all clients connected to a channel.
type ChannelBroadcaster interface {
	Broadcast(channelID string, msg *Message)
}

type ErrorMessage struct {
	CorrelationID string          `json:"correlationId"`
	AppID         string          `json:"appId"`
	UserID        string          `json:"userId"`
	Key           string          `json:"type"`
	OriginKey     string          `json:"originType"`
	OrginBody     json.RawMessage `json:"originBody"`
	ErrorBody     json.RawMessage `json:"errorBody"`
	ChannelID     string          `json:"channelId"`
	ClientID      string          `json:"clientId"`
	Headers       http.Header     `json:"headers"`
}

func (m *Message) ToError(err interface{}) *ErrorMessage {
	bb, _ := json.Marshal(err)
	e := &ErrorMessage{
		CorrelationID: m.CorrelationID,
		AppID:         m.AppID,
		UserID:        m.UserID,
		Key:           MessageError,
		OriginKey:     m.key,
		OrginBody:     m.Body,
		ErrorBody:     bb,
		ChannelID:     m.channelID,
		ClientID:      m.clientID,
		Headers:       m.Headers,
	}
	return e
}

// Bind will decode the error message body to v.
// This message will usually contain further error data and can be specified by the server.
func (e *ErrorMessage) Bind(v interface{}) error {
	if e.ErrorBody == nil {
		return nil
	}
	return json.Unmarshal(e.ErrorBody, &v)
}

// BindOriginBody can be used to decode the body for the original message that triggered
// this error, useful for replaying.
func (e *ErrorMessage) BindOriginBody(v interface{}) error {
	if e.OrginBody == nil {
		return nil
	}
	return json.Unmarshal(e.OrginBody, &v)
}
