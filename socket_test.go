package sockets

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewMessage(t *testing.T) {
	m := NewMessage("test", "clientID", "channelID")
	assert.Equal(t, m.channelID, "channelID")
	assert.Equal(t, m.key, "test")
	assert.Equal(t, m.ClientID, "clientID")
	assert.Empty(t, m.Body)
	assert.Empty(t, m.Expiration)
	assert.Equal(t, m.CorrelationID, "")
	assert.Equal(t, m.Headers, http.Header{})
	assert.Equal(t, m.UserID, "")
	assert.Equal(t, m.AppID, "")
	assert.NotEmpty(t, m.ID())
	assert.NotEqual(t, m.Timestamp(), time.Time{})
}

func TestMessageFrom(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		original *Message
		exp      *Message
		key      string
	}{
		"message should convert correctly": {
			original: &Message{
				AppID:     "my-app",
				UserID:    "1234",
				id:        "123456",
				channelID: "12345",
				timestamp: time.Time{},
				key:       "test",
				Headers:   http.Header{"test": []string{"value"}},
				ClientID:  "124",
			},
			exp: &Message{
				AppID:     "my-app",
				UserID:    "1234",
				id:        "123456",
				channelID: "12345",
				timestamp: time.Time{},
				key:       "new-key",
				Headers:   http.Header{},
				ClientID:  "124",
			},
			key: "new-key",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			m := test.original.NewFrom(test.key)
			assert.Equal(t, m.channelID, test.exp.channelID)
			assert.Equal(t, m.key, test.exp.key)
			assert.Equal(t, m.ClientID, test.exp.ClientID)
			assert.Empty(t, m.Body)
			assert.Empty(t, m.Expiration)
			assert.Equal(t, m.CorrelationID, "")
			assert.Equal(t, m.Headers, http.Header{})
			assert.Equal(t, m.UserID, test.exp.UserID)
			assert.Equal(t, m.AppID, test.exp.AppID)
			assert.NotEmpty(t, m.ID())
			assert.NotEqual(t, m.Timestamp(), time.Time{})
		})
	}
}

type Bodytest struct {
	Test      string `json:"test"`
	My        int    `json:"my"`
	Othertest bool   `json:"othertest"`
}

func TestMessage_Bind(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		msg     *Message
		bytes   []byte
		ExpBody Bodytest
		err     error
	}{
		"body should map correctly": {
			msg:   NewMessage("test", "clientID", "channelID"),
			bytes: []byte(`{"test":"value","my":123,"othertest":true}`),
			ExpBody: Bodytest{
				Test:      "value",
				My:        123,
				Othertest: true,
			},
			err: nil,
		}, "empty body should return no error and body should remain nil": {
			msg: NewMessage("test", "clientID", "channelID"),
			err: nil,
		}, "guff json should error": {
			msg:   NewMessage("test", "clientID", "channelID"),
			bytes: []byte(`{"test value","my":123,"othertest":true}`),
			err:   errors.New("invalid character ',' after object key"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			test.msg.Body = test.bytes
			var body Bodytest
			err := test.msg.Bind(&body)
			if test.err != nil {
				assert.Error(t, err)
				assert.EqualError(t, err, test.err.Error())
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, test.ExpBody, body)
		})
	}
}

func TestMessage_WithBody(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		msg      *Message
		body     *Bodytest
		expBytes []byte
		err      error
	}{
		"successful run should return no errors": {
			msg: NewMessage("test", "clientID", "channelID"),
			body: &Bodytest{
				Test:      "my test",
				My:        122334,
				Othertest: false,
			},
			expBytes: []byte(`{"test":"my test","my":122334,"othertest":false}`),
			err:      nil,
		}, "no body should not error": {
			msg: NewMessage("test", "clientID", "channelID"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := test.msg.WithBody(test.body)
			if test.err != nil {
				assert.Error(t, err)
				assert.EqualError(t, err, test.err.Error())
				return
			}
			assert.NoError(t, err)
			assert.True(t, bytes.Equal(test.expBytes, test.msg.Body))
		})
	}
}

func TestMessage_NoContent(t *testing.T) {
	t.Parallel()
	msg, err := NewMessage("test", "clientID", "channelID").NoContent()
	assert.Nil(t, msg)
	assert.Nil(t, err)
}

func TestMessage_ToError(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		msg     *Message
		exp     *ErrorMessage
		errBody interface{}
		err     error
	}{
		"nil err body should return correctly": {
			msg: NewMessage("test", "clientID", "channelID"),
			exp: &ErrorMessage{
				CorrelationID: "",
				AppID:         "",
				UserID:        "",
				Key:           MessageError,
				OriginKey:     "test",
				OriginBody:    nil,
				ErrorBody:     nil,
				ChannelID:     "channelID",
				ClientID:      "clientID",
				Headers:       http.Header{},
			},
			errBody: nil,
			err:     nil,
		}, "message with body should have body copied to OriginBody": {
			msg: func() *Message {
				msg := NewMessage("test", "clientID", "channelID")
				assert.NoError(t, msg.WithBody(&Bodytest{
					Test:      "viking wizard eyes",
					My:        182,
					Othertest: true,
				}))
				return msg
			}(),
			exp: &ErrorMessage{
				CorrelationID: "",
				AppID:         "",
				UserID:        "",
				Key:           MessageError,
				OriginKey:     "test",
				OriginBody:    []byte(`{"test":"viking wizard eyes","my":182,"othertest":true}`),
				ErrorBody:     nil,
				ChannelID:     "channelID",
				ClientID:      "clientID",
				Headers:       http.Header{},
			},
			errBody: nil,
			err:     nil,
		}, "message with body and an error struct should copy correctly": {
			msg: func() *Message {
				msg := NewMessage("test", "clientID", "channelID")
				assert.NoError(t, msg.WithBody(&Bodytest{
					Test:      "viking wizard eyes",
					My:        182,
					Othertest: true,
				}))
				return msg
			}(),
			exp: &ErrorMessage{
				CorrelationID: "",
				AppID:         "",
				UserID:        "",
				Key:           MessageError,
				OriginKey:     "test",
				OriginBody:    []byte(`{"test":"viking wizard eyes","my":182,"othertest":true}`),
				ErrorBody:     []byte(`{"code":"abc123","reason":"big failure"}`),
				ChannelID:     "channelID",
				ClientID:      "clientID",
				Headers:       http.Header{},
			},
			errBody: struct {
				Code   string `json:"code"`
				Reason string `json:"reason"`
			}{
				Code:   "abc123",
				Reason: "big failure",
			},
			err: nil,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			errMsg := test.msg.ToError(test.errBody)
			assert.Equal(t, test.exp, errMsg)
		})
	}
}

func TestErrorMessage_Bind(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		errMsg    *ErrorMessage
		expDetail ErrorDetail
		err       error
	}{
		"successful run should return no errors": {
			errMsg: &ErrorMessage{
				ErrorBody: []byte(`{"title":"my error","description":"a failure occurred","errCode":"abc"}`),
			},
			expDetail: ErrorDetail{
				Title:       "my error",
				Description: "a failure occurred",
				ErrCode:     "abc",
			},
			err: nil,
		},
		"empty error body should return nil": {
			errMsg: &ErrorMessage{},
			err:    nil,
		}, "guff json should error": {
			errMsg: &ErrorMessage{
				ErrorBody: []byte(`{"title":}`),
			},
			err: errors.New("invalid character '}' looking for beginning of value"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var body ErrorDetail
			err := test.errMsg.Bind(&body)
			if test.err != nil {
				assert.Error(t, err)
				assert.EqualError(t, err, test.err.Error())
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, test.expDetail, body)
		})
	}
}

func TestErrorMessage_BindOriginBody(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		errMsg        *ErrorMessage
		expOriginBody Bodytest
		err           error
	}{
		"successful run should return no errors": {
			errMsg: &ErrorMessage{
				OriginBody: []byte(`{"test":"viking wizard eyes","my":182,"othertest":true}`),
			},
			expOriginBody: Bodytest{
				Test:      "viking wizard eyes",
				My:        182,
				Othertest: true,
			},
			err: nil,
		},
		"empty error body should return nil": {
			errMsg: &ErrorMessage{},
			err:    nil,
		}, "guff json should error": {
			errMsg: &ErrorMessage{
				OriginBody: []byte(`{"title":}`),
			},
			err: errors.New("invalid character '}' looking for beginning of value"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var body Bodytest
			err := test.errMsg.BindOriginBody(&body)
			if test.err != nil {
				assert.Error(t, err)
				assert.EqualError(t, err, test.err.Error())
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, test.expOriginBody, body)
		})
	}
}
