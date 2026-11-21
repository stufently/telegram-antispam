package main

import (
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/ops"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

func TestNewMediaShadowWiresOnlyWhenEnabledWithAKey(t *testing.T) {
	reg := ops.NewRegistry()
	cfg := config.Detection{MediaShadow: config.MediaShadow{Enabled: true, APIKey: "k", Model: "jev-latest", Timeout: config.Duration(3 * time.Second)}}
	s := newMediaShadow(cfg, fake.New(), reg)
	if s == nil || s.Timeout != 3*time.Second || s.Fetch == nil || s.Classify == nil || s.Count == nil {
		t.Fatalf("shadow = %+v", s)
	}
	cfg.MediaShadow.APIKey = ""
	if newMediaShadow(cfg, fake.New(), reg) != nil {
		t.Fatal("wired without a key")
	}
	cfg.MediaShadow.APIKey, cfg.MediaShadow.Enabled = "k", false
	if newMediaShadow(cfg, fake.New(), reg) != nil {
		t.Fatal("wired while disabled")
	}
}
