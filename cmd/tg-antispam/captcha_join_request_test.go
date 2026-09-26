package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
	"github.com/stufently/telegram-antispam/internal/watch"
)

func TestChatJoinRequestRunsCaptcha(t *testing.T) {
	on := true
	off := false
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	port := fake.New()
	port.CaptchaMessageID = 77
	var metrics []string
	c := &watch.Captcha{
		Config: config.NewStore(&config.Config{
			Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: &off},
			Captcha: config.Captcha{
				Enabled: &on, Mode: "join_request", Timeout: durPtr(30 * time.Second),
				OnFail: "kick", Text: strPtr("prove it"), ButtonText: strPtr("I am not a bot"),
			},
		}),
		Store: db,
		Port:  port,
		Now:   func() time.Time { return time.Unix(1_700_000_000, 0) },
		Count: func(r string) { metrics = append(metrics, r) },
	}
	req := &models.ChatJoinRequest{
		Chat:       models.Chat{ID: -100},
		From:       models.User{ID: 7, FirstName: "Ada"},
		UserChatID: 700,
	}
	handleChatJoinRequest(context.Background(), req, func(_ int64, job func()) { job() }, c, func(string) {})
	c.Wait()
	if port.LastCaptchaMessage.Chat != 700 {
		t.Fatal(port.LastCaptchaMessage)
	}
	if len(metrics) != 1 || metrics[0] != string(watch.CaptchaChallenged) {
		t.Fatal(metrics)
	}
	row, found, err := db.GetCaptcha(-100, 7)
	if err != nil || !found || row.State != store.CaptchaChallenged || row.Mode != "join_request" {
		t.Fatal(row, found, err)
	}
}
