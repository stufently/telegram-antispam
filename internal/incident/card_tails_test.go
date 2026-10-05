package incident

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/admin"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

type tailDeletion struct {
	chat int64
	ids  []int
}

type tailMarkup struct {
	card    store.IncidentCard
	buttons [][]telegram.Button
}

// Keep the existing port's effects and capture every rollback target, not
// just the last deletion. The send hook exercises the exact claim window.
type tailPort struct {
	telegram.Port
	reply      string
	duringSend func()
	deletions  []tailDeletion
	markups    []tailMarkup
}

func (p *tailPort) SendAdmin(ctx context.Context, chat int64, msg telegram.AdminMessage) (int, error) {
	mid, err := p.Port.SendAdmin(ctx, chat, msg)
	if p.duringSend != nil {
		f := p.duringSend
		p.duringSend = nil
		f()
	}
	return mid, err
}

func (p *tailPort) AnswerCallback(_ context.Context, _, text string) error {
	p.reply = text
	return nil
}

func (p *tailPort) DeleteMessages(ctx context.Context, chat int64, ids []int) error {
	p.deletions = append(p.deletions, tailDeletion{chat, append([]int(nil), ids...)})
	return p.Port.DeleteMessages(ctx, chat, ids)
}

func (p *tailPort) EditAdminMarkup(ctx context.Context, chat int64, mid int, buttons [][]telegram.Button) error {
	p.markups = append(p.markups, tailMarkup{store.IncidentCard{ChatID: chat, MessageID: mid}, buttons})
	return p.Port.EditAdminMarkup(ctx, chat, mid, buttons)
}

func pressTailCard(t *testing.T, h *admin.Handler, id int64, mid int, act string) {
	t.Helper()
	if err := h.Handle(context.Background(), admin.Callback{
		Data: fmt.Sprintf("%s:%d", act, id), PresserID: 7, AdminChatID: 999, MessageID: mid,
	}); err != nil {
		t.Fatal(err)
	}
}

func requireTailMarkup(t *testing.T, p *tailPort, mid int) {
	t.Helper()
	want := []tailMarkup{{card: store.IncidentCard{ChatID: 999, MessageID: mid}}}
	if !reflect.DeepEqual(p.markups, want) {
		t.Fatalf("want buttons removed only from card %d in chat 999, got %+v", mid, p.markups)
	}
}

func requireTailCard(t *testing.T, db *store.DB, id int64, mid int) {
	t.Helper()
	var got store.IncidentCard
	if err := db.Read().QueryRow("SELECT chat_id, message_id FROM incident_cards WHERE incident_id=?", id).Scan(&got.ChatID, &got.MessageID); err != nil {
		t.Fatal(err)
	}
	if got != (store.IncidentCard{ChatID: 999, MessageID: mid}) {
		t.Fatalf("registered card = %+v, want chat 999 message %d", got, mid)
	}
}

func TestManualOverrideHoldsClaimUntilNewCard(t *testing.T) {
	db, f, m := newOverrideRig(t)
	f.SendAdminID = 10
	if err := m.Handle(context.Background(), autoIncident(true)); err != nil {
		t.Fatal(err)
	}
	id := onlyIncidentID(t, db)
	requireTailCard(t, db, id, 10)
	p := &tailPort{Port: f}
	m = New(p, db, 999)
	m.SetButtons(admin.Buttons)
	h := admin.NewHandler(p, db, map[int64]bool{7: true})
	trained := 0
	h.SetTrainer(func(int64, string, []string) error { trained++; return nil })
	pressed := false
	p.duringSend = func() {
		pressed = true
		pressTailCard(t, h, id, 10, "fp")
		if trained != 0 || count(f.Calls(), "UnrestrictMember") != 0 {
			t.Fatalf("old card acted while override sent its new card: trained=%d calls=%v", trained, f.Calls())
		}
		if got := readAudit(t, db, id).decision; got != store.ManualOverrideClaim {
			t.Fatalf("override claim lost during send: %q", got)
		}
	}
	f.SendAdminID = 20
	fresh, err := m.HandleReport(context.Background(), manualIncident("manual_spam", domain.ActionDeleteMute))
	if err != nil || !fresh || !pressed {
		t.Fatalf("override: fresh=%v pressed=%v err=%v", fresh, pressed, err)
	}
	requireTailCard(t, db, id, 20)
	if got := readAudit(t, db, id).decision; got != "" {
		t.Fatalf("override did not release claim: %q", got)
	}
	p.markups = nil
	pressTailCard(t, h, id, 10, "fp")
	if !strings.Contains(p.reply, "устарело") || trained != 0 || count(f.Calls(), "UnrestrictMember") != 0 {
		t.Fatalf("old card remained usable: reply=%q trained=%d calls=%v", p.reply, trained, f.Calls())
	}
	requireTailMarkup(t, p, 10)
	pressTailCard(t, h, id, 20, "fp")
	if trained != 1 || count(f.Calls(), "UnrestrictMember") != 1 {
		t.Fatalf("new card must undo and train once: trained=%d calls=%v", trained, f.Calls())
	}
}

type tailFailRecording struct {
	*store.DB
	fail    error
	records []store.IncidentCard
}

func (r *tailFailRecording) SaveIncidentCard(id int64, card store.IncidentCard) error {
	if r.fail != nil {
		err := r.fail
		r.fail = nil
		return err
	}
	if err := r.DB.SaveIncidentCard(id, card); err != nil {
		return err
	}
	r.records = append(r.records, card)
	return nil
}

func TestUnrecordedCardIsRolledBack(t *testing.T) {
	db, f, _, _, edited := newEditRig(t)
	id := onlyIncidentID(t, db)
	oldChat, oldIDs, err := db.ListEvidence(id)
	if err != nil {
		t.Fatal(err)
	}
	p := &tailPort{Port: f}
	r := &tailFailRecording{DB: db, fail: errors.New("card write unavailable")}
	m := New(p, r, 999)
	m.SetButtons(admin.Buttons)
	f.SendAdminID = 20
	if _, err := m.HandleReport(context.Background(), edited); err == nil || !strings.Contains(err.Error(), "card write unavailable") {
		t.Fatalf("must report original registration failure: %v", err)
	}
	if count(f.Calls(), "RestrictMember") != 0 {
		t.Fatalf("unrecorded card authorized sanction: %v", f.Calls())
	}
	// newEditRig's first copy is 101055; only the second copy (102055)
	// and its unregistered card may be removed by the failed promotion.
	want := []tailDeletion{{999, []int{20}}, {999, []int{102055}}}
	if !reflect.DeepEqual(p.deletions, want) {
		t.Fatalf("rollback targets = %+v, want %+v", p.deletions, want)
	}
	requireTailCard(t, db, id, 0)
	chat, ids, err := db.ListEvidence(id)
	if err != nil || chat != oldChat || !reflect.DeepEqual(ids, oldIDs) {
		t.Fatalf("old evidence changed: chat=%d ids=%v err=%v", chat, ids, err)
	}
	if got := readAudit(t, db, id).decision; got != "" {
		t.Fatalf("failed promotion kept claim: %q", got)
	}
	f.SendAdminID = 30
	requireEditPromotion(t, m, f, edited)
	requireTailCard(t, db, id, 30)
	if count(f.Calls(), "RestrictMember") != 1 || !reflect.DeepEqual(r.records, []store.IncidentCard{{ChatID: 999, MessageID: 30}}) {
		t.Fatalf("retry must sanction and register once: calls=%v records=%v", f.Calls(), r.records)
	}
	chat, ids, err = db.ListEvidence(id)
	if err != nil || chat != 999 || !reflect.DeepEqual(ids, []int{101055, 103055}) {
		t.Fatalf("retry evidence includes a rolled-back copy: chat=%d ids=%v err=%v", chat, ids, err)
	}
	h := admin.NewHandler(p, db, map[int64]bool{7: true})
	pressTailCard(t, h, id, 0, "fp")
	if !strings.Contains(p.reply, "устарело") || count(f.Calls(), "UnrestrictMember") != 0 {
		t.Fatalf("old card accepted after retry: %q calls=%v", p.reply, f.Calls())
	}
	requireTailMarkup(t, p, 0)
}

func TestUnrecordedCardLosesButtonsWhenDeleteFails(t *testing.T) {
	db, f, _, _, edited := newEditRig(t)
	p := &tailPort{Port: f}
	m := New(p, &tailFailRecording{DB: db, fail: errors.New("card write unavailable")}, 999)
	m.SetButtons(admin.Buttons)
	f.SendAdminID = 20
	f.DeleteErr = errors.New("delete unavailable")
	if _, err := m.HandleReport(context.Background(), edited); err == nil {
		t.Fatal("registration failure must stop promotion")
	}
	requireTailMarkup(t, p, 20)
	if count(f.Calls(), "RestrictMember") != 0 {
		t.Fatalf("failed rollback authorized sanction: %v", f.Calls())
	}
}

func TestDecidedIncidentOldCardIsStale(t *testing.T) {
	db, f, m, _, edited := newEditRig(t)
	f.SendAdminID = 20
	requireEditPromotion(t, m, f, edited)
	id := onlyIncidentID(t, db)
	p := &tailPort{Port: f}
	h := admin.NewHandler(p, db, map[int64]bool{7: true})
	trained := 0
	h.SetTrainer(func(int64, string, []string) error { trained++; return nil })
	pressTailCard(t, h, id, 20, "confirm")
	if trained != 1 || !strings.Contains(p.reply, "confirmed spam") {
		t.Fatalf("confirm failed: reply=%q trained=%d", p.reply, trained)
	}
	for _, act := range []string{"fp", "lift"} {
		p.markups = nil
		pressTailCard(t, h, id, 0, act)
		if !strings.Contains(p.reply, "устарело") || trained != 1 || count(f.Calls(), "UnrestrictMember") != 0 {
			t.Fatalf("decided old card %s: reply=%q trained=%d calls=%v", act, p.reply, trained, f.Calls())
		}
		requireTailMarkup(t, p, 0)
	}
	p.markups = nil
	pressTailCard(t, h, id, 20, "fp")
	if p.reply != "already decided: confirmed spam" || len(p.markups) != 0 || trained != 1 || count(f.Calls(), "UnrestrictMember") != 0 {
		t.Fatalf("current card decision lost: reply=%q markups=%v trained=%d calls=%v", p.reply, p.markups, trained, f.Calls())
	}
}

func TestDecidedLegacyCardStillAlreadyDecided(t *testing.T) {
	db, f, _ := newOverrideRig(t)
	inc := autoIncident(true)
	id, fresh, err := db.InsertPending(inc.ChatID, inc.MessageIDs[0], inc.Sender.UserID, 0, inc.DryRun, inc.Verdict)
	if err != nil || !fresh {
		t.Fatalf("legacy insert: fresh=%v err=%v", fresh, err)
	}
	if claimed, _, err := db.RecordDecision(id, string(admin.ActConfirmSpam)); err != nil || !claimed {
		t.Fatalf("legacy decision: claimed=%v err=%v", claimed, err)
	}
	p := &tailPort{Port: f}
	h := admin.NewHandler(p, db, map[int64]bool{7: true})
	pressTailCard(t, h, id, 10, "fp")
	if p.reply != "already decided: confirmed spam" || len(f.Calls()) != 0 || len(p.markups) != 0 {
		t.Fatalf("legacy response changed: reply=%q calls=%v markups=%v", p.reply, f.Calls(), p.markups)
	}
}

func TestPromotionInFlightKeepsNewCardButtons(t *testing.T) {
	db, f, _, _, edited := newEditRig(t)
	id := onlyIncidentID(t, db)
	p := &tailPort{Port: f}
	m := New(p, db, 999)
	m.SetButtons(admin.Buttons)
	h := admin.NewHandler(p, db, map[int64]bool{7: true})
	trained := 0
	h.SetTrainer(func(int64, string, []string) error { trained++; return nil })
	pressed := false
	p.duringSend = func() {
		// Telegram has published card 20, the machine has not registered it.
		pressed = true
		pressTailCard(t, h, id, 20, "fp")
		if p.reply != "already decided: enforced" || len(p.markups) != 0 || trained != 0 {
			t.Fatalf("in-flight card judged stale: reply=%q markups=%+v trained=%d", p.reply, p.markups, trained)
		}
	}
	f.SendAdminID = 20
	requireEditPromotion(t, m, f, edited)
	if !pressed {
		t.Fatal("send hook did not run")
	}
	requireTailCard(t, db, id, 20)
	pressTailCard(t, h, id, 20, "fp")
	if trained != 1 || count(f.Calls(), "UnrestrictMember") != 1 || len(p.markups) != 0 {
		t.Fatalf("new card must stay usable: trained=%d markups=%+v calls=%v", trained, p.markups, f.Calls())
	}
}
