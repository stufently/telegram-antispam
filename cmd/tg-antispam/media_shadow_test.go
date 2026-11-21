package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"reflect"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/mediashadow"

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

// withMediaLabel may only add a signal: action, reason and review-only stay,
// and the caller's signal slice is not written through.
func TestWithMediaLabelOnlyAddsTheSignal(t *testing.T) {
	f := fake.New()
	f.FileErr = errors.New("download refused")
	s := &mediashadow.Shadow{Fetch: f, Classify: mediashadow.JEV{APIKey: "k", Endpoint: "http://127.0.0.1:1"}}
	base := make([]domain.Signal, 1, 2)
	base[0] = domain.Signal{Name: "captionless_media"}
	v := domain.Verdict{Action: domain.ActionQuarantine, Reason: "captionless_media", ReviewOnly: true, Signals: base}
	m := domain.Message{MediaKinds: []string{"photo"}, PhotoFileID: "id"}
	if got := withMediaLabel(context.Background(), s, m, v); !reflect.DeepEqual(got, v) {
		t.Fatalf("failure changed the verdict: %+v", got)
	}
	if f.LastDownload != "id" {
		t.Fatal("shadow did not run")
	}
}

type fixedScores struct{ s mediashadow.Scores }

func (f fixedScores) Classify(context.Context, []byte, string) (mediashadow.Scores, error) {
	return f.s, nil
}

func TestWithMediaLabelAppendsWithoutTouchingTheCall(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	f.File = buf.Bytes()
	s := &mediashadow.Shadow{Fetch: f, Classify: fixedScores{mediashadow.Scores{Promo: 1, Contact: 1}}}
	base := make([]domain.Signal, 1, 2)
	base[0] = domain.Signal{Name: "captionless_media"}
	v := domain.Verdict{Action: domain.ActionQuarantine, Reason: "captionless_media", ReviewOnly: true, Signals: base}
	got := withMediaLabel(context.Background(), s, domain.Message{MediaKinds: []string{"photo"}, PhotoFileID: "id"}, v)
	if got.Action != domain.ActionQuarantine || got.Reason != "captionless_media" || !got.ReviewOnly || got.Confidence != 0 {
		t.Fatalf("label changed the call: %+v", got)
	}
	if len(got.Signals) != 2 || got.Signals[1].Name != "jev_image" || got.Signals[1].Detail != "promo=1.00 contact=1.00 ordinary=0.00" {
		t.Fatalf("signals = %+v", got.Signals)
	}
	if base[:2][1] != (domain.Signal{}) {
		t.Fatal("appended into the caller's backing array")
	}
}
