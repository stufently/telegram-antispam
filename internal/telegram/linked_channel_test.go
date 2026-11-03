package telegram_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// A channel post that is not an automatic forward is classified as an outside
// channel. When that channel is the chat's linked one, an actionable verdict
// must not reach the machine: BanSenderChat would bar the chat's own channel
// from its discussion group.
func TestLinkedChannelCommentIsNotModerated(t *testing.T) {
	const chat, linked, other = int64(-100500), int64(-100777), int64(-100888)
	lookupErr := errors.New("getChat failed")
	cases := []struct {
		name       string
		sender     int64
		resolver   func(context.Context, int64) (int64, error)
		wantBanned bool
	}{
		{"linked channel", linked, func(_ context.Context, c int64) (int64, error) {
			if c != chat {
				t.Errorf("lookup for chat %d, want %d", c, chat)
			}
			return linked, nil
		}, false},
		{"other channel", other, func(context.Context, int64) (int64, error) { return linked, nil }, true},
		{"chat without linked channel", other, func(context.Context, int64) (int64, error) { return 0, nil }, true},
		{"lookup failed", linked, func(context.Context, int64) (int64, error) { return 0, lookupErr }, true},
		{"no resolver", linked, nil, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(); err != nil {
				t.Fatal(err)
			}
			f := fake.New()
			f.SendAdminID = 5
			m := incident.New(f, db, 999)
			cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
			seq := telegram.NewSequencer()
			h := telegram.NewHandler(db, seq, cfg, m)
			h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
				return domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, true
			})
			if tc.resolver != nil {
				h.SetLinkedChat(tc.resolver)
			}
			h.OnMessage(context.Background(), int64(i+1), domain.Message{
				ChatID: chat, MessageID: 70 + i, Text: "spam",
				Sender: domain.Sender{UserID: 136817688, SenderChatID: tc.sender, Kind: domain.SenderExternalChannel},
			})
			seq.Wait()

			banned := false
			for _, c := range f.Calls() {
				if c == "BanSenderChat" || c == "CopyMessages" {
					banned = true
				}
			}
			if banned != tc.wantBanned {
				t.Fatalf("moderated=%v, want %v; calls=%v", banned, tc.wantBanned, f.Calls())
			}
		})
	}
}

// The lookup is spent only on actionable outside-channel verdicts; a user's
// message never asks for it.
func TestLinkedChannelLookupOnlyForChannelSenders(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	f.SendAdminID = 5
	m := incident.New(f, db, 999)
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
	seq := telegram.NewSequencer()
	h := telegram.NewHandler(db, seq, cfg, m)
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
		return domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, true
	})
	lookups := 0
	h.SetLinkedChat(func(context.Context, int64) (int64, error) { lookups++; return 0, nil })
	// A user's message, even one carrying a sender chat id, is not a channel post.
	h.OnMessage(context.Background(), 1, domain.Message{ChatID: -100501, MessageID: 80, Sender: domain.Sender{UserID: 7, SenderChatID: -100777, Kind: domain.SenderUser}})
	seq.Wait()
	if lookups != 0 {
		t.Fatalf("lookup ran %d times for a user's message", lookups)
	}

	// A channel post the cascade does not act on needs no lookup either.
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) { return domain.Verdict{}, true })
	h.OnMessage(context.Background(), 2, domain.Message{ChatID: -100501, MessageID: 81, Sender: domain.Sender{UserID: 136817688, SenderChatID: -100777, Kind: domain.SenderExternalChannel}})
	seq.Wait()
	if lookups != 0 {
		t.Fatalf("lookup ran %d times for a clean channel post", lookups)
	}
}
