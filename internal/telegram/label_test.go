package telegram_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// The labeller runs after the last gate that can drop an incident, so a
// disabled chat sends nothing out, and its signal reaches the card.
func TestLabelRunsOnlyForIncidentsThatGetACard(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(); err != nil {
			t.Fatal(err)
		}
		const chat = int64(-100600)
		if err := db.UpsertChat(store.ChatRow{ChatID: chat, Enabled: true, DryRun: true}); err != nil {
			t.Fatal(err)
		}
		if disabled {
			if err := db.DisableChat(chat); err != nil {
				t.Fatal(err)
			}
		}
		f := fake.New()
		f.SendAdminID = 5
		m := incident.New(f, db, 999)
		cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionDeleteMute, Chats: config.ChatsPolicy{Mode: "auto"}})
		seq := telegram.NewSequencer()
		h := telegram.NewHandler(db, seq, cfg, m)
		h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
			return domain.Verdict{Action: domain.ActionQuarantine, Reason: "captionless_media", ReviewOnly: true,
				Signals: []domain.Signal{{Name: "captionless_media"}}}, true
		})
		labelled := 0
		h.SetLabel(func(_ context.Context, msg domain.Message, v domain.Verdict) domain.Verdict {
			labelled++
			if msg.PhotoFileID != "file-1" || !v.ReviewOnly {
				t.Errorf("label got msg=%+v verdict=%+v", msg, v)
			}
			v.Signals = append(v.Signals, domain.Signal{Name: "jev_image", Detail: "promo=0.97 contact=0.10 ordinary=0.02"})
			return v
		})
		h.OnMessage(context.Background(), 1, domain.Message{ChatID: chat, MessageID: 90, MediaKinds: []string{"photo"}, PhotoFileID: "file-1",
			Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser}})
		seq.Wait()
		_ = db.Close()
		if disabled {
			if labelled != 0 {
				t.Fatalf("labelled a message from a disabled chat")
			}
			continue
		}
		if labelled != 1 || !strings.Contains(f.LastAdmin.Text, "JEV photo (shadow, no effect): ad 0.97") {
			t.Fatalf("labelled=%d card=%q", labelled, f.LastAdmin.Text)
		}
	}
}
