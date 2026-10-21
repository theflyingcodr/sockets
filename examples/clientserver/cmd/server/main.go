package main

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/labstack/echo-contrib/v5/echoprometheus"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	smw "github.com/theflyingcodr/sockets/middleware"

	"github.com/theflyingcodr/clientserver"
)

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	e := echo.New()
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())
	e.Use(echoprometheus.NewMiddleware("echo"))
	e.GET("/metrics", echoprometheus.NewHandler())
	// setup the socket server
	s := clientserver.SetupServer()
	defer s.Close()

	// add middleware, with panic going first
	s.WithMiddleware(smw.PanicHandler, smw.Timeout(smw.NewTimeoutConfig()), smw.Metrics())
	// this is our websocket endpoint, clients will hit this with the channelID they wish to connect to
	e.GET("/ws/:channelID", clientserver.WsHandler(s))

	// Cancelling ctx on interrupt makes Start shut the server down gracefully,
	// waiting up to 10 seconds for in-flight requests.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	sc := echo.StartConfig{Address: ":9323", HideBanner: true, GracefulTimeout: 10 * time.Second}
	if err := sc.Start(ctx, e); err != nil {
		log.Err(err).Msg("closed echo")
	}
}
