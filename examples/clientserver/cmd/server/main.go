package main

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	smw "github.com/theflyingcodr/sockets/middleware"

	"github.com/theflyingcodr/sockets/examples/clientserver"
)

type TestMessage struct {
	When time.Time `json:"when"`
	Test string    `json:"test"`
}

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// setup the socket server
	s := clientserver.SetupServer()
	defer s.Close()

	// add middleware, with panic going first
	s.WithMiddleware(smw.PanicHandler, smw.Timeout(smw.NewTimeoutConfig()))

	// this is our websocket endpoint, clients will hit this with the channelID they wish to connect to
	e.GET("/ws/:channelID", clientserver.WsHandler(s))
	go func() {
		log.Err(e.Start(":1323")).Msg("closed echo")
	}()

	// Wait for interrupt signal to gracefully shutdown the server with a timeout of 10 seconds.
	// Use a buffered channel to avoid missing signals as recommended for signal.Notify
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		log.Err(err).Msg("shutdown echo")
	}
}
