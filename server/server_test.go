package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/theflyingcodr/sockets"
	"github.com/theflyingcodr/sockets/server"
)

// newTestServer serves every request on channel "test" of a SocketServer built with opts.
func newTestServer(t *testing.T, opts ...server.OptFunc) (*server.SocketServer, *httptest.Server) {
	t.Helper()
	s := server.New(opts...)
	var up websocket.Upgrader
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		_ = s.Listen(ws, "test")
	}))
	t.Cleanup(srv.Close)
	return s, srv
}

// dial connects to srv and consumes the join.success message.
func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), http.Header{"Origin": {srv.URL}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var join sockets.Message
	require.NoError(t, c.ReadJSON(&join))
	require.Equal(t, sockets.MessageJoinSuccess, join.Key())
	return c
}

// readUntil reads from c until a message with key arrives, failing on any read error.
func readUntil(t *testing.T, c *websocket.Conn, key string) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var m sockets.Message
		require.NoError(t, c.ReadJSON(&m), "waiting for %q", key)
		if m.Key() == key {
			return
		}
	}
}

// readUntilClosed drains c until the server closes it and returns how long that took.
func readUntilClosed(t *testing.T, c *websocket.Conn, limit time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	_ = c.SetReadDeadline(start.Add(limit))
	for {
		var m sockets.Message
		if err := c.ReadJSON(&m); err != nil {
			took := time.Since(start)
			require.Less(t, took, limit, "connection not closed within %s: %v", limit, err)
			return took
		}
	}
}

func broadcast(s *server.SocketServer, key string, n int) {
	for i := 0; i < n; i++ {
		msg := sockets.NewMessage(key, "", "test")
		_ = msg.WithBody(map[string]int{"n": i})
		s.Broadcast("test", msg)
	}
}

// A client that never reads fills its send buffer. The server must drop it rather
// than block the shared broadcast loop, so a client joining afterwards still gets
// broadcasts. Before the fix the loop blocked on the full buffer and this hung.
func TestSocketServer_SlowClientIsDroppedNotStalled(t *testing.T) {
	s, srv := newTestServer(t)

	slow := dial(t, srv)
	broadcast(s, "flood", 3000)
	readUntilClosed(t, slow, 5*time.Second)

	fresh := dial(t, srv)
	broadcast(s, "sentinel", 1)
	readUntil(t, fresh, "sentinel")
}

// A reader that keeps up receives a large burst in order without the writer
// panicking. Before the fix the writer drained its buffer with a goroutine per
// message and gorilla panicked on the concurrent writes.
func TestSocketServer_BurstIsWrittenSerially(t *testing.T) {
	s, srv := newTestServer(t)
	c := dial(t, srv)

	const n = 200 // under the 256 send buffer so the reader is never dropped
	go broadcast(s, "burst", n)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for want := 0; want < n; {
		var m sockets.Message
		require.NoError(t, c.ReadJSON(&m))
		if m.Key() != "burst" {
			continue
		}
		var body map[string]int
		require.NoError(t, m.Bind(&body))
		require.Equal(t, want, body["n"], "messages out of order")
		want++
	}
}

func TestSocketServer_IdleTimeout(t *testing.T) {
	const idle = 300 * time.Millisecond

	t.Run("closes a client that sends nothing", func(t *testing.T) {
		_, srv := newTestServer(t, server.WithIdleTimeout(idle))
		c := dial(t, srv)

		took := readUntilClosed(t, c, 3*time.Second)
		require.GreaterOrEqual(t, took, idle/2, "closed before the idle timeout")
		require.Less(t, took, 2*time.Second, "not closed after the idle timeout")
	})

	t.Run("stays open while the client sends", func(t *testing.T) {
		_, srv := newTestServer(t, server.WithIdleTimeout(idle))
		c := dial(t, srv)

		closed := make(chan time.Time, 1)
		go func() {
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				var m sockets.Message
				if err := c.ReadJSON(&m); err != nil {
					closed <- time.Now()
					return
				}
			}
		}()

		start := time.Now()
		for i := 0; i < 5; i++ { // 500ms of activity, longer than idle
			require.NoError(t, c.WriteJSON(sockets.NewMessage("ping", "", "test")))
			time.Sleep(100 * time.Millisecond)
		}
		active := time.Since(start)

		select {
		case at := <-closed:
			require.GreaterOrEqual(t, at.Sub(start), active, "closed while the client was still sending")
		case <-time.After(3 * time.Second):
			t.Fatal("not closed after the client went idle")
		}
	})

	t.Run("is off by default", func(t *testing.T) {
		_, srv := newTestServer(t)
		c := dial(t, srv)

		_ = c.SetReadDeadline(time.Now().Add(2 * idle))
		var m sockets.Message
		err := c.ReadJSON(&m)
		var netErr interface{ Timeout() bool }
		require.ErrorAs(t, err, &netErr)
		require.True(t, netErr.Timeout(), "want our read deadline to expire, got %v", err)
	})
}
