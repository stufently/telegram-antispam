package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/admin"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
	"github.com/stufently/telegram-antispam/internal/train"
)

type routingPort struct {
	*fake.Fake
	started, release chan struct{}
	once             sync.Once
}

func (p *routingPort) RestrictMember(ctx context.Context, chat, user int64, perms telegram.Perms, until int64) error {
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return p.Fake.RestrictMember(ctx, chat, user, perms, until)
}
func routingDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "routing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAdminCallbackWaitsForInitialSanctionAndCommandTraining(t *testing.T) {
	db := routingDB(t)
	p := &routingPort{Fake: fake.New(), started: make(chan struct{}), release: make(chan struct{})}
	p.SendAdminID = 91
	seq := telegram.NewSequencer()
	var release sync.Once
	unblock := func() { release.Do(func() { close(p.release) }) }
	defer func() { unblock(); seq.Wait() }()
	machine := incident.New(p, db, 999)
	handler := admin.NewHandler(p, db, map[int64]bool{50: true})
	handler.SetTrainer(feedbackTrainer(db, func(int64) string { return "global" }, "user"))
	ctx := context.Background()
	callbackDone := make(chan error, 1)
	router := newAdminCallbackRouter(db, seq, func(cb admin.Callback) { callbackDone <- handler.Handle(ctx, cb) }, func(cb admin.Callback, text string) { t.Errorf("router answered %q itself", text) })
	defer router.Wait()
	tokens := []string{"offer", "work"}
	seq.Submit(-100, func() {
		err := machine.Handle(ctx, domain.Incident{ChatID: -100, MessageIDs: []int{10}, Sender: domain.Sender{Kind: domain.SenderUser, UserID: 7}, Tokens: tokens, Verdict: domain.Verdict{Action: domain.ActionDeleteMute}})
		if err != nil {
			t.Error(err)
		}
		// Commands.handleSpam trains AFTER HandleReport and must finish before fp.
		if _, _, err := train.RelabelTokens(db, "global", "ham", "spam", "user", tokens); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-p.started:
	case <-time.After(3 * time.Second):
		t.Fatal("sanction not started")
	}
	var id int64
	if err := db.Read().QueryRow("SELECT id FROM incidents WHERE chat_id=-100").Scan(&id); err != nil {
		t.Fatal(err)
	}
	router.Submit(admin.Callback{ID: "fp", Data: fmt.Sprintf("fp:%d", id), PresserID: 50, AdminChatID: 999, MessageID: 91})
	// Routing must drain BEFORE source queues close, including during shutdown.
	router.Wait()
	var decision string
	if err := db.Read().QueryRow("SELECT decision FROM incidents WHERE id=?", id).Scan(&decision); err != nil {
		t.Fatal(err)
	}
	if decision != "" {
		t.Errorf("callback claimed unfinished incident: %q", decision)
	}
	select {
	case err := <-callbackDone:
		t.Errorf("callback ran before sanction completed: %v", err)
	default:
	}
	unblock()
	seq.Wait()
	select {
	case err := <-callbackDone:
		if err != nil {
			t.Error(err)
		}
	default:
		t.Error("accepted callback lost during drain")
	}
	undos := 0
	for _, call := range p.Calls() {
		if call == "UnrestrictMember" {
			undos++
		}
	}
	if undos != 1 {
		t.Errorf("unrestrict calls=%d, want 1", undos)
	}
	spam, ham, _, _, err := db.BayesTotals("global")
	if err != nil || spam != 0 || ham != 1 {
		t.Errorf("spam=%d ham=%d err=%v", spam, ham, err)
	}
}

func TestFeedbackTrainerRelabelsBothDirectionsAndScopes(t *testing.T) {
	for _, scope := range []string{"global", "chat:-100"} {
		t.Run(scope, func(t *testing.T) {
			db := routingDB(t)
			tokens := []string{"one", "two"}
			if _, err := train.RecordTokens(db, scope, "spam", "user", tokens); err != nil {
				t.Fatal(err)
			}
			learn := feedbackTrainer(db, func(int64) string { return scope }, "user")
			for _, label := range []string{"ham", "ham", "spam", "spam"} {
				if err := learn(-100, label, tokens); err != nil {
					t.Fatal(err)
				}
				spam, ham, _, _, err := db.BayesTotals(scope)
				if err != nil || (label == "ham" && (spam != 0 || ham != 1)) || (label == "spam" && (spam != 1 || ham != 0)) {
					t.Fatalf("label=%s spam=%d ham=%d err=%v", label, spam, ham, err)
				}
			}
		})
	}
}

func TestDecideRelabelsPreviousHam(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "")
	tokens := []string{"offer", "work"}
	id := pendingWithTokens(t, db, -100, tokens)
	if _, err := train.RecordTokens(db, "global", "ham", "user", tokens); err != nil {
		t.Fatal(err)
	}
	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, fmt.Sprint(id)}, openDB); err != nil {
		t.Fatal(err)
	}
	spam, ham, _, _, err := db.BayesTotals("global")
	if err != nil || spam != 1 || ham != 0 {
		t.Fatalf("spam=%d ham=%d err=%v", spam, ham, err)
	}
}

func TestAdminRouterDoesNotSerializeDifferentSourceChats(t *testing.T) {
	db := routingDB(t)
	one, _, _ := db.InsertPending(-100, 1, 7, 0, true, domain.Verdict{Action: domain.ActionQuarantine})
	two, _, _ := db.InsertPending(-200, 1, 8, 0, true, domain.Verdict{Action: domain.ActionQuarantine})
	seq := telegram.NewSequencer()
	blocked, release := make(chan struct{}), make(chan struct{})
	seq.Submit(-100, func() { close(blocked); <-release })
	<-blocked
	results := make(chan string, 2)
	router := newAdminCallbackRouter(db, seq, func(cb admin.Callback) { results <- cb.ID }, func(cb admin.Callback, text string) { t.Errorf("%s answered %q", cb.ID, text) })
	router.Submit(admin.Callback{ID: "one", Data: fmt.Sprintf("fp:%d", one)})
	router.Submit(admin.Callback{ID: "two", Data: fmt.Sprintf("fp:%d", two)})
	router.Wait()
	select {
	case got := <-results:
		if got != "two" {
			t.Errorf("got %s before first chat released", got)
		}
	case <-time.After(time.Second):
		t.Error("other chat was blocked")
	}
	close(release)
	seq.Wait()
	select {
	case got := <-results:
		if got != "one" {
			t.Error(got)
		}
	default:
		t.Error("first callback lost")
	}
}

func TestAdminRouterLookupFailureNeverDispatchesWithoutSourceOrdering(t *testing.T) {
	db := routingDB(t)
	// A failed lookup cannot fall back to Handle: its own retry might succeed
	// and mutate an incident on the routing queue instead of the source queue.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	seq := telegram.NewSequencer()
	defer seq.Wait()
	called := make(chan struct{}, 1)
	answers := make(chan string, 1)
	router := newAdminCallbackRouter(db, seq, func(admin.Callback) { called <- struct{}{} }, func(_ admin.Callback, text string) { answers <- text })
	router.Submit(admin.Callback{Data: "fp:1"})
	router.Wait()
	select {
	case <-called:
		t.Fatal("lookup failure dispatched without source scope")
	default:
	}
	select {
	case got := <-answers:
		if !strings.Contains(got, "again") {
			t.Errorf("answer %q does not invite a retry", got)
		}
	default:
		t.Error("lookup failure left the callback unanswered")
	}
}

func TestAdminRouterAnswersUnknownIncident(t *testing.T) {
	db := routingDB(t)
	seq := telegram.NewSequencer()
	defer seq.Wait()
	answers := make(chan string, 1)
	router := newAdminCallbackRouter(db, seq, func(admin.Callback) { t.Error("dispatched an unknown incident") }, func(_ admin.Callback, text string) { answers <- text })
	router.Submit(admin.Callback{Data: "fp:4242"})
	router.Wait()
	select {
	case got := <-answers:
		if got != "incident not found" {
			t.Errorf("answer %q", got)
		}
	default:
		t.Error("unknown incident left the callback unanswered")
	}
}

// A raid fills the source chat's queue exactly when moderators press buttons.
// The press is dropped then, but it must be answered rather than lost.
func TestAdminRouterAnswersWhenSourceQueueIsFull(t *testing.T) {
	db := routingDB(t)
	id, _, _ := db.InsertPending(-100, 1, 7, 0, true, domain.Verdict{Action: domain.ActionQuarantine})
	seq := telegram.NewSequencer()
	blocked, release := make(chan struct{}), make(chan struct{})
	seq.Submit(-100, func() { close(blocked); <-release })
	<-blocked
	for seq.TrySubmit(-100, func() {}) {
	}
	answers := make(chan string, 1)
	router := newAdminCallbackRouter(db, seq, func(admin.Callback) { t.Error("dropped callback ran") }, func(_ admin.Callback, text string) { answers <- text })
	router.Submit(admin.Callback{ID: "raid", Data: fmt.Sprintf("fp:%d", id)})
	router.Wait()
	close(release)
	seq.Wait()
	select {
	case got := <-answers:
		if got != busyText {
			t.Errorf("answer %q", got)
		}
	default:
		t.Error("dropped callback left unanswered")
	}
}
