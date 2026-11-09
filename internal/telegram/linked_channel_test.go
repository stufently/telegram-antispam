package telegram_test

import (
	"bytes"
	"context"
	"errors"
	"log"
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
		// A failed lookup defers: the sender may be the chat's own channel.
		{"lookup failed", linked, func(context.Context, int64) (int64, error) { return 0, lookupErr }, false},
		{"lookup failed, other channel", other, func(context.Context, int64) (int64, error) { return 0, lookupErr }, false},
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

// The lookup is spent only on actionable outside-channel verdicts: neither a
// user's message (even one carrying a sender chat id) nor a channel post the
// cascade does not act on asks for it.
func TestLinkedChannelLookupOnlyForActionableChannelPosts(t *testing.T) {
	cases := []struct {
		name    string
		verdict domain.Verdict
		sender  domain.Sender
	}{
		{"user message", domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, domain.Sender{UserID: 7, SenderChatID: -100777, Kind: domain.SenderUser}},
		{"clean channel post", domain.Verdict{Action: domain.ActionNone}, domain.Sender{UserID: 136817688, SenderChatID: -100777, Kind: domain.SenderExternalChannel}},
	}
	for _, tc := range cases {
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
			decided := false
			h.SetDecide(func(domain.Message) (domain.Verdict, bool) { decided = true; return tc.verdict, true })
			lookups := 0
			h.SetLinkedChat(func(context.Context, int64) (int64, error) { lookups++; return 0, nil })
			h.OnMessage(context.Background(), 1, domain.Message{ChatID: -100501, MessageID: 80, Sender: tc.sender})
			seq.Wait()
			if !decided {
				t.Fatal("precondition: message never reached decide")
			}
			if lookups != 0 {
				t.Fatalf("lookup ran %d times", lookups)
			}
		})
	}
}

// A linked-channel lookup that fails or times out defers moderation the way
// an unresolved admin list does: no sanction, no incident, no admin card,
// and a "moderation deferred" line naming the cause. Banning on a guess could
// bar the chat's own channel from its discussion group.
func TestLinkedChannelLookupFailureDefersModeration(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"error", errors.New("getChat failed")},
		{"timeout", context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			prev, prevFlags := log.Writer(), log.Flags()
			log.SetOutput(&logs)
			log.SetFlags(0)
			defer func() { log.SetOutput(prev); log.SetFlags(prevFlags) }()

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
			h.SetLinkedChat(func(context.Context, int64) (int64, error) { lookups++; return 0, tc.err })
			h.OnMessage(context.Background(), 1, domain.Message{
				ChatID: -100500, MessageID: 90, Text: "spam",
				Sender: domain.Sender{UserID: 136817688, SenderChatID: -100777, Kind: domain.SenderExternalChannel},
			})
			seq.Wait()

			if lookups != 1 {
				t.Fatalf("precondition: lookup ran %d times, want 1", lookups)
			}
			if calls := f.Calls(); len(calls) != 0 {
				t.Fatalf("deferred message reached Telegram: calls=%v", calls)
			}
			want := "chat=-100500 msg=90: moderation deferred: linked channel lookup unavailable: " + tc.err.Error()
			if !strings.Contains(logs.String(), want) {
				t.Fatalf("log missing %q; got:\n%s", want, logs.String())
			}
		})
	}
}
