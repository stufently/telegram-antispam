// External test package (not `package telegram`) so this test can inject a
// real *incident.Machine — package incident imports package telegram, so an
// internal test file here (package telegram) importing incident would be an
// import cycle Go disallows in tests. See assert_test.go for the same
// pattern. Handler.decide is unexported, so this test drives it through the
// exported Handler.SetDecide test/wiring hook instead of a field literal.
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

func TestOnMessageDrivesMachineWhenVerdictActionable(t *testing.T) {
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
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto"}})
	seq := telegram.NewSequencer()
	h := telegram.NewHandler(db, seq, cfg, m)
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
		return domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, true
	})
	h.OnMessage(context.Background(), 1, domain.Message{ChatID: -100123, MessageID: 55, Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser}})
	seq.Wait()

	calls := f.Calls()
	var iCopy, iBan = -1, -1
	for i, c := range calls {
		if c == "CopyMessages" && iCopy < 0 {
			iCopy = i
		}
		if c == "BanMember" && iBan < 0 {
			iBan = i
		}
	}
	if iCopy < 0 || iBan < 0 || iCopy > iBan {
		t.Fatalf("expected evidence copy before ban; calls=%v", calls)
	}
}

func TestOnEditedMessageDrivesMachineWhenVerdictActionable(t *testing.T) {
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
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto"}})
	seq := telegram.NewSequencer()
	h := telegram.NewHandler(db, seq, cfg, m)
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
		return domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, true
	})
	h.OnEditedMessage(context.Background(), 1, domain.Message{ChatID: -100124, MessageID: 56, Sender: domain.Sender{UserID: 8, Kind: domain.SenderUser}})
	seq.Wait()

	calls := f.Calls()
	var iCopy, iBan = -1, -1
	for i, c := range calls {
		if c == "CopyMessages" && iCopy < 0 {
			iCopy = i
		}
		if c == "BanMember" && iBan < 0 {
			iBan = i
		}
	}
	if iCopy < 0 || iBan < 0 || iCopy > iBan {
		t.Fatalf("expected evidence copy before ban; calls=%v", calls)
	}
}

func TestOnMessageAlbumFlushBuildsOneIncident(t *testing.T) {
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
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto"}})
	seq := telegram.NewSequencer()
	h := telegram.NewHandler(db, seq, cfg, m)
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
		return domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, true
	})
	defer h.Stop()

	chatID := int64(-100125)
	h.OnMessage(context.Background(), 10, domain.Message{ChatID: chatID, MessageID: 61, MediaGroupID: "g1", Sender: domain.Sender{UserID: 9, Kind: domain.SenderUser}})
	h.OnMessage(context.Background(), 11, domain.Message{ChatID: chatID, MessageID: 62, MediaGroupID: "g1", Sender: domain.Sender{UserID: 9, Kind: domain.SenderUser}})
	time.Sleep(900 * time.Millisecond) // let the 700ms album window fire before draining the sequencer
	seq.Wait()

	calls := f.Calls()
	nCopy := 0
	for _, c := range calls {
		if c == "CopyMessages" {
			nCopy++
		}
	}
	if nCopy != 1 {
		t.Fatalf("expected a single evidence copy for the whole album, got %d (calls=%v)", nCopy, calls)
	}
}
