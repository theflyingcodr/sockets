package clientserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/theflyingcodr/sockets"
	"github.com/theflyingcodr/sockets/client"
)

func SetupClient() *client.Client {
	u := url.URL{Scheme: "ws", Host: "localhost:9323", Path: "/ws"}
	log.Info().Msgf("connecting to %s", u.String())
	h := http.Header{}
	h.Add("test", "value")
	c := client.New()

	c.RegisterListener(sockets.MessageInfo, func(ctx context.Context, msg *sockets.Message) (*sockets.Message, error) {
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

	for i := 0; i < 2000; i++ {
		if err := c.JoinChannel(u.String(), fmt.Sprintf("test-channel-%d", i), h, nil); err != nil {
			log.Fatal().Err(err).Msg("CLIENT failed to join channel")
		}
		go func(id int) {
			for {
				log.Debug().Msg("sending messages")
				time.Sleep(time.Millisecond * 250)
				if err := c.Publish(sockets.Request{
					ChannelID:  fmt.Sprintf("test-channel-%d", id),
					MessageKey: "test",
					Body: TestMessage{
						When: time.Now().UTC(),
						Test: fmt.Sprintf("%d", id),
					},
					Headers: h,
				}); err != nil {
					log.Err(err).Msg("failed to publish")
				}

			}
		}(i)
	}

	return c
}

type Info struct {
	TotalConnections int `json:"totalConnections"`
	TotalChannels    int `json:"totalChannels"`
}
