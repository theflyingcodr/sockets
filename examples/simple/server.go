package main

import (
	"context"
	"errors"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"

	"github.com/theflyingcodr/sockets"
)

var (
	upgrader = websocket.Upgrader{}
)

func wsHandler(svr *sockets.SocketServer) echo.HandlerFunc {
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

func setupServer() *sockets.SocketServer {
	s := sockets.NewSocketServer()
	s.WithInfo().
		RegisterChannelHandler("test", func(ctx context.Context, msg *sockets.Message) (*sockets.Message, error) {
			log.Info().Msg("SERVER received new test message")
			var req TestMessage
			if err := msg.Bind(&req); err != nil {
				return nil, err
			}
			log.Info().Msgf("%+v", req)
			resp := msg.NewFrom("test.resp")
			return resp, errors.New("I failed so bad")
		})
	return s
}
