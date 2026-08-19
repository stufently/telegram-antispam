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
	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// boolPtr returns a fresh *bool so each test owns its own value (no shared
// mutable aliasing). start_in_dry_run now defaults to true, so enforcement
// tests must opt out explicitly with boolPtr(false).
func boolPtr(b bool) *bool { return &b }

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
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
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
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
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
	cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
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

// TestOnMessageWithCascadeDecideDrivesMachineOnStopword wires a real
// detect.Cascade (not a stub) as the decide hook, the way main will, and
// checks a deny-stopword message reaches the machine end to end.
func TestOnMessageWithCascadeDecideDrivesMachineOnStopword(t *testing.T) {
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

	cascade := detect.Cascade{
		Trust:          db, // real store: this user has no trust history, so untrusted
		Hist:           detect.NewMemHistory(),
		Rules:          detect.Rules{DenyStopwords: []string{"spamword"}},
		Behavior:       detect.BehaviorCfg{},
		TrustThreshold: 5,
		DefaultAction:  domain.ActionBan,
		DefaultScope:   domain.ScopeGlobal,
	}
	h.SetDecide(func(msg domain.Message) (domain.Verdict, bool) {
		return cascade.Decide(msg, false)
	})

	h.OnMessage(context.Background(), 1, domain.Message{
		ChatID:    -100200,
		MessageID: 70,
		Text:      "buy spamword now",
		Sender:    domain.Sender{UserID: 11, Kind: domain.SenderUser},
	})
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
		t.Fatalf("expected the cascade's deny-stopword hit to drive evidence copy before ban; calls=%v", calls)
	}
}

// TestOnEditedMessageCascadeSeesEditedTrue checks that edited=true actually
// reaches detect.Cascade.Decide for the OnEditedMessage path, via
// SetEditedDecide. The message text itself ("hi") triggers no hard rule and
// no behavioral flood check — only the cascade's FlagEdits check, which
// fires only when it observes edited=true — so the machine being driven
// proves the edited flag made it all the way through.
func TestOnEditedMessageCascadeSeesEditedTrue(t *testing.T) {
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

	cascade := detect.Cascade{
		Trust:          db,
		Hist:           detect.NewMemHistory(),
		Rules:          detect.Rules{},
		Behavior:       detect.BehaviorCfg{FlagEdits: true},
		TrustThreshold: 5,
		DefaultAction:  domain.ActionBan,
		DefaultScope:   domain.ScopeGlobal,
	}
	h.SetDecide(func(msg domain.Message) (domain.Verdict, bool) {
		return cascade.Decide(msg, false)
	})
	h.SetEditedDecide(func(msg domain.Message) (domain.Verdict, bool) {
		return cascade.Decide(msg, true)
	})

	h.OnEditedMessage(context.Background(), 1, domain.Message{
		ChatID:    -100201,
		MessageID: 71,
		Text:      "hi",
		Sender:    domain.Sender{UserID: 12, Kind: domain.SenderUser},
	})
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
		t.Fatalf("expected FlagEdits to fire (proving edited=true reached Decide) and drive the machine; calls=%v", calls)
	}
}

// TestOnEditedMessageFallsBackToDecideWithoutEditedHook checks that when
// SetEditedDecide is never called, OnEditedMessage still uses the SetDecide
// hook (pre-M3 behavior, and what existing tests that only call SetDecide
// rely on).
func TestOnEditedMessageFallsBackToDecideWithoutEditedHook(t *testing.T) {
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

	h.OnEditedMessage(context.Background(), 1, domain.Message{ChatID: -100202, MessageID: 72, Sender: domain.Sender{UserID: 13, Kind: domain.SenderUser}})
	seq.Wait()

	calls := f.Calls()
	found := false
	for _, c := range calls {
		if c == "BanMember" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected SetDecide hook to drive the machine when SetEditedDecide was never called; calls=%v", calls)
	}
}

// TestOnMessageBumpsTrustForNonActionableMeaningfulMessage checks the
// wiring-level trust bump: a non-actionable, meaningful message from a real
// user increments the store's trust counter. This bump happens inside
// process (a sequencer job), off the poll path — Decide itself never bumps
// trust (it is pure).
func TestOnMessageBumpsTrustForNonActionableMeaningfulMessage(t *testing.T) {
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
		return domain.Verdict{}, false
	})

	h.OnMessage(context.Background(), 1, domain.Message{
		ChatID:    -100300,
		MessageID: 80,
		Text:      "hello there, friend",
		Sender:    domain.Sender{UserID: 21, Kind: domain.SenderUser},
	})
	seq.Wait()

	count, err := db.TrustCount(-100300, 21)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected trust bumped to 1, got %d", count)
	}
}

// TestOnMessageDoesNotBumpTrustWhenActionable checks the trust bump is
// skipped when the verdict is actionable (the whole point of the trust gate
// is to withhold trust from a message that already got flagged).
func TestOnMessageDoesNotBumpTrustWhenActionable(t *testing.T) {
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

	h.OnMessage(context.Background(), 1, domain.Message{
		ChatID:    -100301,
		MessageID: 81,
		Text:      "hello there, friend",
		Sender:    domain.Sender{UserID: 22, Kind: domain.SenderUser},
	})
	seq.Wait()

	count, err := db.TrustCount(-100301, 22)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected trust not bumped for an actionable message, got %d", count)
	}
}

func TestOnMessageAcceptedWorkUsesLifecycleContext(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	m := incident.New(f, db, 999)
	cfg := config.NewStore(&config.Config{Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
	seq := telegram.NewSequencer()
	h := telegram.NewHandler(db, seq, cfg, m)
	lifecycleCtx, stopLifecycle := context.WithCancel(context.Background())
	defer stopLifecycle()
	h.SetContext(lifecycleCtx)
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
		return domain.Verdict{Action: domain.ActionBan, Confidence: 1}, true
	})
	updateCtx, cancelUpdate := context.WithCancel(context.Background())
	cancelUpdate()

	h.OnMessage(updateCtx, 1, domain.Message{ChatID: 1, MessageID: 1, Sender: domain.Sender{Kind: domain.SenderUser, UserID: 7}})
	seq.Wait()

	for _, call := range f.Calls() {
		if call == "BanMember" {
			return
		}
	}
	t.Fatalf("accepted work was canceled with its update context; calls=%v", f.Calls())
}

func TestOnMessageAdminLookupUnavailableDoesNotBumpTrust(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewStore(&config.Config{Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(false)}})
	seq := telegram.NewSequencer()
	h := telegram.NewHandler(db, seq, cfg, incident.New(fake.New(), db, 999))
	h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
		return domain.Verdict{Action: domain.ActionNone, Reason: detect.ReasonAdminLookupUnavailable}, false
	})

	h.OnMessage(context.Background(), 1, domain.Message{
		ChatID: 1, MessageID: 1, Text: "meaningful message", Sender: domain.Sender{Kind: domain.SenderUser, UserID: 7},
	})
	seq.Wait()

	count, err := db.TrustCount(1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deferred moderation must not grant trust, got %d", count)
	}
}

// TestChatsEnforceTakesRegisteredChatLive covers the config-side flip that
// replaces "edit the SQLite file to stop dry-running". A chat registers
// under start_in_dry_run: true, is observed, and is then taken live by a
// single line of config — the stored row never changes.
func TestChatsEnforceTakesRegisteredChatLive(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	// deliver runs one message through a fresh Handler/Sequencer against the
	// SAME database — Sequencer.Wait closes the sequencer for good (it is the
	// shutdown drain), so each phase needs its own. The shared db is the
	// point: it carries the stored chat row across phases.
	deliver := func(policy config.ChatsPolicy, updateID int64, messageID int) []string {
		f := fake.New()
		f.SendAdminID = 5
		m := incident.New(f, db, 999)
		cfg := config.NewStore(&config.Config{AdminChatID: 999, Action: domain.ActionBan, Chats: policy})
		seq := telegram.NewSequencer()
		h := telegram.NewHandler(db, seq, cfg, m)
		h.SetDecide(func(domain.Message) (domain.Verdict, bool) {
			return domain.Verdict{Action: domain.ActionBan, Confidence: 0.99}, true
		})
		h.OnMessage(context.Background(), updateID, domain.Message{
			ChatID: -100123, MessageID: messageID,
			Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser},
			Text:   "free casino bonus",
		})
		seq.Wait()
		return f.Calls()
	}
	banned := func(calls []string) bool {
		for _, c := range calls {
			if c == "BanMember" {
				return true
			}
		}
		return false
	}

	observed := config.ChatsPolicy{Mode: "auto", StartInDryRun: boolPtr(true)}
	if calls := deliver(observed, 1, 55); banned(calls) {
		t.Fatalf("dry-run chat applied a sanction; calls=%v", calls)
	}
	if row, found, _ := db.GetChat(-100123); !found || !row.DryRun {
		t.Fatalf("stored chat row = %+v, want a dry-run row", row)
	}

	// Same stored row, one config line added.
	enforced := observed
	enforced.Enforce = []int64{-100123}
	if calls := deliver(enforced, 2, 56); !banned(calls) {
		t.Fatalf("chats.enforce did not take the chat live; calls=%v", calls)
	}
	if row, _, _ := db.GetChat(-100123); !row.DryRun {
		t.Fatal("config override wrote back to the stored row, which must stay the record of where the chat started")
	}

	// force_dry_run wins, so a chat can be pulled back without first
	// removing its enforce entry.
	braked := enforced
	braked.ForceDryRun = []int64{-100123}
	if calls := deliver(braked, 3, 57); banned(calls) {
		t.Fatalf("force_dry_run did not override enforce; calls=%v", calls)
	}
}

// TestIncidentCapturesTokensForAdminFeedback pins the other half of the
// admin-button fix: the tokens an admin's later Confirm-spam press trains on
// must be captured while the message still exists, because the incident
// machine deletes the original at the end of Handle.
func TestIncidentCapturesTokensForAdminFeedback(t *testing.T) {
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
	h.OnMessage(context.Background(), 1, domain.Message{ChatID: -100123, MessageID: 55, Sender: domain.Sender{UserID: 7, Kind: domain.SenderUser}, Text: "free casino bonus"})
	seq.Wait()

	tokens, ok, err := db.GetIncidentTokens(1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(tokens) == 0 {
		t.Fatalf("no tokens captured for the incident (ok=%v tokens=%v)", ok, tokens)
	}
}
