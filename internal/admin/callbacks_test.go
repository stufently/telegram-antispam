package admin

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// newMigrated builds a fresh, migrated store.DB backed by a temp file, for
// tests that need a real incident row (Handle looks up the incident via
// GetIncident to resolve RBAC scope and what there is to undo).
func newMigrated(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// trainerCall records one invocation of a fake Trainer.
type trainerCall struct {
	scope, label, text string
}

func TestAuthorizedOperatorAndChatAdmin(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 50}}
	h := NewHandler(f, nil, map[int64]bool{7: true})

	ok, _ := h.Authorized(context.Background(), -100123, 7) // global operator
	if !ok {
		t.Fatal("global operator must be authorized")
	}
	ok, _ = h.Authorized(context.Background(), -100123, 50) // source-chat admin
	if !ok {
		t.Fatal("source-chat admin must be authorized")
	}
	ok, _ = h.Authorized(context.Background(), -100123, 99) // neither
	if ok {
		t.Fatal("non-admin non-operator must be rejected")
	}
}

func TestParseCallbackRoundTrip(t *testing.T) {
	btns := Buttons("abc123")
	// first button data must parse back
	act, key, ok := ParseCallback(btns[0][0].Data)
	if !ok || key != "abc123" || act == "" {
		t.Fatalf("parse: act=%q key=%q ok=%v", act, key, ok)
	}
}

// actedIncident inserts an incident that really applied action, at
// StateDone, and stores tokens for it — the state an admin sees when the
// evidence message shows up with buttons.
func actedIncident(t *testing.T, db *store.DB, chatID int64, msgID int, userID int64, action domain.Action, tokens []string) int64 {
	t.Helper()
	id, _, err := db.InsertPending(chatID, msgID, userID, 0, false, domain.Verdict{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetIncidentState(id, domain.StateDone); err != nil {
		t.Fatal(err)
	}
	if len(tokens) > 0 {
		if err := db.SaveIncidentTokens(id, tokens); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestHandleConfirmSpamTrainsFromStoredTokens(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionDeleteMute, []string{"casino", "bonus"})

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true}) // presser 7 is a global operator

	var calls []trainerCall
	h.SetTrainer(func(scope, label string, tokens []string) error {
		calls = append(calls, trainerCall{scope, label, strings.Join(tokens, " ")})
		return nil
	})

	cb := Callback{
		ID:        "cbid1",
		Data:      encode(ActConfirmSpam, strconv.FormatInt(incidentID, 10)),
		PresserID: 7,
	}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 1 {
		t.Fatalf("trainer calls = %d, want 1 (calls=%v)", len(calls), calls)
	}
	want := trainerCall{"global", "spam", "casino bonus"}
	if calls[0] != want {
		t.Fatalf("trainer call = %+v, want %+v", calls[0], want)
	}
	// Reviewed incidents must not keep message-derived content around.
	if _, ok, err := db.GetIncidentTokens(incidentID); err != nil || ok {
		t.Fatalf("tokens after review: ok=%v err=%v, want dropped", ok, err)
	}
	// Confirming spam is not an undo: no sanction call may be issued.
	for _, c := range f.Calls() {
		if c == "UnbanMember" || c == "UnrestrictMember" {
			t.Fatalf("confirm spam issued %s, want no sanction change", c)
		}
	}
}

func TestHandleConfirmSpamUnauthorizedDoesNotTrain(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionDeleteMute, []string{"casino", "bonus"})

	f := fake.New()
	f.Admins = nil                           // no chat admins, and presser is not a global operator
	h := NewHandler(f, db, map[int64]bool{}) // no global operators

	var calls []trainerCall
	h.SetTrainer(func(scope, label string, tokens []string) error {
		calls = append(calls, trainerCall{scope, label, strings.Join(tokens, " ")})
		return nil
	})

	cb := Callback{
		ID:        "cbid2",
		Data:      encode(ActConfirmSpam, strconv.FormatInt(incidentID, 10)),
		PresserID: 99, // unauthorized
	}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 0 {
		t.Fatalf("trainer calls = %d, want 0 for unauthorized presser (calls=%v)", len(calls), calls)
	}
	if _, ok, _ := db.GetIncidentTokens(incidentID); !ok {
		t.Fatal("unauthorized press dropped the incident tokens")
	}
}

func TestHandleFalsePositiveUnmutesAndTrainsHam(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionDeleteMute, []string{"hello", "everyone"})

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{9: true})

	var calls []trainerCall
	h.SetTrainer(func(scope, label string, tokens []string) error {
		calls = append(calls, trainerCall{scope, label, strings.Join(tokens, " ")})
		return nil
	})

	cb := Callback{ID: "cb", Data: encode(ActFalsePositive, strconv.FormatInt(incidentID, 10)), PresserID: 9}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	if f.LastUnrestrict.UserID != 7 || f.LastUnrestrict.Chat != -100123 {
		t.Fatalf("unmute targeted chat=%d user=%d, want chat=-100123 user=7", f.LastUnrestrict.Chat, f.LastUnrestrict.UserID)
	}
	// Must be UnrestrictMember, not a permissive RestrictMember: the latter
	// cannot express invite/pin/react rights, so it would half-lift the mute.
	for _, c := range f.Calls() {
		if c == "RestrictMember" {
			t.Fatal("undo used RestrictMember, which leaves non-send permissions revoked")
		}
	}
	if len(calls) != 1 || calls[0].label != "ham" {
		t.Fatalf("trainer calls = %v, want one ham call", calls)
	}
}

func TestHandleFalsePositiveUnbansWhenActionWasBan(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionBan, nil)

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{9: true})

	cb := Callback{ID: "cb", Data: encode(ActFalsePositive, strconv.FormatInt(incidentID, 10)), PresserID: 9}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if f.LastUnban.UserID != 7 || f.LastUnban.Chat != -100123 {
		t.Fatalf("unban targeted chat=%d user=%d, want chat=-100123 user=7", f.LastUnban.Chat, f.LastUnban.UserID)
	}
}

func TestHandleUndoSkippedForDryRunIncident(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	// dry_run=true: nothing was ever applied, so there is nothing to lift.
	incidentID, _, err := db.InsertPending(-100123, 55, 7, 0, true, domain.Verdict{Action: domain.ActionBan})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetIncidentState(incidentID, domain.StateDone); err != nil {
		t.Fatal(err)
	}

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{9: true})

	cb := Callback{ID: "cb", Data: encode(ActLiftNoLearn, strconv.FormatInt(incidentID, 10)), PresserID: 9}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c == "UnbanMember" || c == "UnrestrictMember" {
			t.Fatalf("dry-run incident issued %s, want no Telegram sanction call", c)
		}
	}
}

func TestHandleLiftDoesNotTrain(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionMute, []string{"borderline", "text"})

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{9: true})
	var calls []trainerCall
	h.SetTrainer(func(scope, label string, tokens []string) error {
		calls = append(calls, trainerCall{scope, label, strings.Join(tokens, " ")})
		return nil
	})

	cb := Callback{ID: "cb", Data: encode(ActLiftNoLearn, strconv.FormatInt(incidentID, 10)), PresserID: 9}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("lift trained %v, want no training", calls)
	}
	if f.LastUnrestrict.UserID != 7 {
		t.Fatal("lift did not restore permissions")
	}
	if _, ok, _ := db.GetIncidentTokens(incidentID); ok {
		t.Fatal("lift kept the captured tokens")
	}
}

func TestHandleDeleteEvidenceRemovesCopies(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionDeleteMute, nil)
	if err := db.AddEvidence(incidentID, -100999, []int{11, 12}); err != nil {
		t.Fatal(err)
	}

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{9: true})

	cb := Callback{ID: "cb", Data: encode(ActDeleteEvidence, strconv.FormatInt(incidentID, 10)), PresserID: 9}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if f.LastDelete.Chat != -100999 || len(f.LastDelete.IDs) != 2 {
		t.Fatalf("deleted chat=%d ids=%v, want chat=-100999 ids=[11 12]", f.LastDelete.Chat, f.LastDelete.IDs)
	}
	// A second press must not re-issue deletes for ids Telegram forgot.
	f.LastDelete.Chat, f.LastDelete.IDs = 0, nil
	if err := h.Handle(context.Background(), Callback{ID: "cb2", Data: cb.Data, PresserID: 9}); err != nil {
		t.Fatal(err)
	}
	if f.LastDelete.IDs != nil {
		t.Fatalf("second press deleted %v, want nothing", f.LastDelete.IDs)
	}
}

func TestHandleSecondDecisionIsRefused(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionBan, []string{"casino"})

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{9: true})
	var trained int
	h.SetTrainer(func(string, string, []string) error { trained++; return nil })

	key := strconv.FormatInt(incidentID, 10)
	if err := h.Handle(context.Background(), Callback{ID: "a", Data: encode(ActConfirmSpam, key), PresserID: 9}); err != nil {
		t.Fatal(err)
	}
	before := len(f.Calls())

	// The buttons live in the admin chat forever. A later press — the same
	// one again, or the opposite verdict — must not act: it could otherwise
	// lift a newer, unrelated sanction against the same user.
	if err := h.Handle(context.Background(), Callback{ID: "b", Data: encode(ActFalsePositive, key), PresserID: 9}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls()[before:] {
		if c == "UnbanMember" || c == "UnrestrictMember" {
			t.Fatalf("second decision issued %s", c)
		}
	}
	if trained != 1 {
		t.Fatalf("trainer ran %d times, want 1 (the claimed decision only)", trained)
	}
}

func TestHandleDeleteEvidenceStillWorksAfterADecision(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionDeleteMute, nil)
	if err := db.AddEvidence(incidentID, -100999, []int{11}); err != nil {
		t.Fatal(err)
	}

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{9: true})
	key := strconv.FormatInt(incidentID, 10)
	if err := h.Handle(context.Background(), Callback{ID: "a", Data: encode(ActConfirmSpam, key), PresserID: 9}); err != nil {
		t.Fatal(err)
	}
	// Cleaning up copied evidence is housekeeping, not a verdict, so the
	// one-decision guard must not block it.
	if err := h.Handle(context.Background(), Callback{ID: "b", Data: encode(ActDeleteEvidence, key), PresserID: 9}); err != nil {
		t.Fatal(err)
	}
	if f.LastDelete.Chat != -100999 {
		t.Fatalf("evidence not deleted after a decision (chat=%d)", f.LastDelete.Chat)
	}
}

// TestUndoLiftsAChannelBan covers the sanction that has no member behind it.
// A channel posting into the group is banned with banChatSenderChat, and the
// incident's user_id is 0 — so an undo keyed on the user would silently do
// nothing and leave the channel banned with no way back through the buttons.
func TestUndoLiftsAChannelBan(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	id, _, err := db.InsertPending(-100123, 55, 0, -1009999, false, domain.Verdict{Action: domain.ActionBan})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetIncidentState(id, domain.StateDone); err != nil {
		t.Fatal(err)
	}

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true})
	cb := Callback{ID: "cb", Data: encode(ActLiftNoLearn, strconv.FormatInt(id, 10)), PresserID: 7}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	var lifted bool
	for _, c := range f.Calls() {
		if c == "UnbanSenderChat" {
			lifted = true
		}
		if c == "UnbanMember" {
			t.Fatalf("a channel ban must not be lifted with UnbanMember; calls=%v", f.Calls())
		}
	}
	if !lifted {
		t.Fatalf("channel ban was not lifted; calls=%v", f.Calls())
	}
}

// TestFailedUndoCanBeRetried: the one-decision claim is taken before the
// Telegram call, so a transient API error must not leave the incident marked
// decided with the sanction still in place.
func TestFailedUndoCanBeRetried(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	id := actedIncident(t, db, -100123, 55, 7, domain.ActionBan, nil)

	f := fake.New()
	f.UnbanErr = errors.New("telegram is having a moment")
	h := NewHandler(f, db, map[int64]bool{7: true})
	cb := Callback{ID: "cb", Data: encode(ActLiftNoLearn, strconv.FormatInt(id, 10)), PresserID: 7}
	if err := h.Handle(context.Background(), cb); err == nil {
		t.Fatal("a failing unban must surface as an error")
	}

	// Second press, this time with Telegram cooperating.
	f.UnbanErr = nil
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatalf("retry after a failed undo: %v", err)
	}
	var unbanned int
	for _, c := range f.Calls() {
		if c == "UnbanMember" {
			unbanned++
		}
	}
	if unbanned != 2 {
		t.Fatalf("UnbanMember called %d times, want 2 (one failed, one retried)", unbanned)
	}
}
