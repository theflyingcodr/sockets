package main

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type TestMessage struct {
	When time.Time `json:"when"`
	Test string    `json:"test"`
}

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	e := echo.New()
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	s := setupServer()
	defer s.Close()
	e.GET("/ws/:channelID", wsHandler(s))

	// Cancelling ctx on interrupt makes Start shut the server down gracefully,
	// waiting up to 10 seconds for in-flight requests.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := echo.StartConfig{Address: ":1323", HideBanner: true, GracefulTimeout: 10 * time.Second}
		if err := sc.Start(ctx, e); err != nil {
			log.Err(err).Msg("closed echo")
		}
	}()
	c := setupClient()
	defer c.Close()

	<-done
}
