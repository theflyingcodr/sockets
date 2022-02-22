package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/theflyingcodr/sockets"
	sockets2 "github.com/theflyingcodr/sockets/client"
)

func setupClient() *sockets2.Client {
	u := url.URL{Scheme: "ws", Host: "localhost:1323", Path: "/ws"}
	log.Info().Msgf("connecting to %s", u.String())
	h := http.Header{}
	h.Add("test", "value")
	client := sockets2.New()

	client.RegisterListener(sockets.MessageInfo, func(ctx context.Context, msg *sockets.Message) (*sockets.Message, error) {
		log.Info().Msg("CLIENT received new info response")
		var req Info
		if err := msg.Bind(&req); err != nil {
			return nil, err
		}
		log.Info().
			Int("totalConnections", req.TotalConnections).
			Int("totalChannels", req.TotalConnections).
			Msg("CLIENT info received")
		return nil, nil
	})
	client.RegisterListener("test.resp", func(ctx context.Context, msg *sockets.Message) (*sockets.Message, error) {
		log.Info().Msgf("CLIENT received: %+v", msg)
		return msg.NoContent()
	})

	if err := client.JoinChannel(u.String(), "test-channel", h, nil); err != nil {
		log.Fatal().Err(err).Msg("CLIENT failed to join channel")
	}
	go func() {
		i := 0
		for {
			log.Debug().Msg("sending messages")
			time.Sleep(time.Millisecond * 500)

			if err := client.Publish(sockets.Request{
				ChannelID:  "test-channel",
				MessageKey: "test",
				Body: TestMessage{
					When: time.Now().UTC(),
					Test: fmt.Sprintf("%d", i),
				},
				Headers: h,
			}); err != nil {
				log.Fatal().Err(err).Msg("failed to publish")
			}

			if err := client.Publish(sockets.Request{
				ChannelID:  "test-channel",
				MessageKey: sockets.MessageGetInfo,
				Headers:    h,
			}); err != nil {
				log.Fatal().Err(err).Msg("failed to publish")
			}
			i++
		}
	}()

	return client
}

type Info struct {
	TotalConnections int `json:"totalConnections"`
	TotalChannels    int `json:"totalChannels"`
}
