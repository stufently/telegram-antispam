package telegram_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// New messages from real users are recorded for a later delete_mute
// cleanup; edits and channel posts are not.
func TestHandlerRecordsRecentMessages(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionDeleteMute, Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
	seq := telegram.NewSequencer()
	h := telegram.NewHandler(db, seq, cfg, incident.New(f, db, 999))
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) { return domain.Verdict{}, false })
	type rec struct {
		chat, user int64
		id         int
		sent       int64
	}
	var got []rec
	h.SetRecentRecorder(func(chat, user int64, id int, sent time.Time) { got = append(got, rec{chat, user, id, sent.Unix()}) })
	before := time.Now().Unix()

	user := domain.Sender{UserID: 7, Kind: domain.SenderUser}
	h.OnMessage(context.Background(), 1, domain.Message{ChatID: -100, MessageID: 10, Text: "hello there", Sender: user, Date: 1763000000})
	h.OnEditedMessage(context.Background(), 2, domain.Message{ChatID: -100, MessageID: 10, Text: "edited", Sender: user})
	h.OnMessage(context.Background(), 3, domain.Message{ChatID: -100, MessageID: 11, Text: "post",
		Sender: domain.Sender{UserID: 136817688, SenderChatID: -100555, Kind: domain.SenderExternalChannel}})
	h.OnMessage(context.Background(), 4, domain.Message{ChatID: -100, MessageID: 12, Text: "again", Sender: user})
	seq.Wait()

	// Telegram's Date when present; processing time only without one.
	if len(got) != 2 || got[0] != (rec{-100, 7, 10, 1763000000}) ||
		got[1].chat != -100 || got[1].user != 7 || got[1].id != 12 || got[1].sent < before {
		t.Fatalf("recorded %v", got)
	}
}
