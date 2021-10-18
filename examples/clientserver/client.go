package clientserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/theflyingcodr/sockets"
)

func SetupClient() *sockets.Client {
	u := url.URL{Scheme: "ws", Host: "localhost:1323", Path: "/ws"}
	log.Info().Msgf("connecting to %s", u.String())
	h := http.Header{}
	h.Add("test", "value")
	client := sockets.NewClient()

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
	}).RegisterListener("test.resp", func(ctx context.Context, msg *sockets.Message) (*sockets.Message, error) {
		log.Info().Msgf("CLIENT received: %+v", msg)
		return msg.NoContent()
	})

	for i := 0; i < 999; i++ {
		if err := client.JoinChannel(u.String(), fmt.Sprintf("test-channel-%d", i), h); err != nil {
			log.Fatal().Err(err).Msg("CLIENT failed to join channel")
		}
		go func(id int) {
			for {
				log.Debug().Msg("sending messages")
				time.Sleep(time.Millisecond * 500)

				if err := client.Publish(fmt.Sprintf("test-channel-%d", id), "test", TestMessage{
					When: time.Now().UTC(),
					Test: fmt.Sprintf("%d", id),
				}, h); err != nil {
					log.Err(err).Msg("failed to publish")
				}

				if err := client.Publish(fmt.Sprintf("test-channel-%d", id), sockets.MessageGetInfo, nil, h); err != nil {
					log.Err(err).Msg("failed to publish")
				}
			}
		}(i)
	}

	return client
}

type Info struct {
	TotalConnections int `json:"totalConnections"`
	TotalChannels    int `json:"totalChannels"`
}
