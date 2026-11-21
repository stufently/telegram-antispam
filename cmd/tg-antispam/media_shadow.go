package main

import (
	"log"
	"net/http"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/mediashadow"
	"github.com/stufently/telegram-antispam/internal/ops"
)

// newMediaShadow builds the captionless-photo shadow, or nil when it is off.
// A missing key disables it with a log line rather than failing startup: the
// shadow decides nothing, so it is the last thing that should keep the bot
// from moderating.
func newMediaShadow(cfg config.Detection, fetch mediashadow.Fetcher, reg *ops.Registry) *mediashadow.Shadow {
	ms := cfg.MediaShadow
	if !ms.Enabled {
		return nil
	}
	if ms.APIKey == "" {
		log.Printf("media shadow: enabled but no JEV key (JEV_TOKEN / api_key), not wired")
		return nil
	}
	log.Printf("media shadow enabled: captionless photos are labelled by JEV (model=%s, timeout=%s), label only",
		ms.Model, ms.Timeout.Duration())
	return &mediashadow.Shadow{
		Fetch:    fetch,
		Classify: mediashadow.JEV{APIKey: ms.APIKey, Model: ms.Model, Endpoint: ms.APIBase, Client: http.DefaultClient},
		Timeout:  ms.Timeout.Duration(),
		Count: func(result string) {
			reg.IncCounter("tg_antispam_media_shadow_total", 1, "result", result)
		},
	}
}
