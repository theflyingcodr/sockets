package clientserver

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"

	"github.com/theflyingcodr/sockets"
	sockets2 "github.com/theflyingcodr/sockets/server"
)

var (
	upgrader = websocket.Upgrader{}
)

func WsHandler(svr *sockets2.SocketServer) echo.HandlerFunc {
	return func(c echo.Context) error {
		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()
		svr.Listen(ws, c.Param("channelID"))

		log.Info().Msgf("exiting listener")
		return nil
	}
}

func SetupServer() *sockets2.SocketServer {
	s := sockets2.NewSocketServer()
	s.WithInfo().
		RegisterChannelHandler("test", func(ctx context.Context, msg *sockets.Message) (*sockets.Message, error) {
			log.Info().Msg("SERVER received new test message")
			var req TestMessage
			if err := msg.Bind(&req); err != nil {
				return nil, err
			}
			log.Info().Msgf("%+v", req)
			resp := msg.NewFrom("test.resp")
			// setup a random failure
			var err error
			if rand.Int()%5 == 0 {
				err = errors.New("test handler failed")
			}
			return resp, err
		})
	return s
}

type TestMessage struct {
	When time.Time `json:"when"`
	Test string    `json:"test"`
}
