package admin

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// reportedIncident inserts an incident that was raised but never acted on —
// a review verdict, or any incident from a chat still in dry-run. This is the
// only state the enforce button is for.
func reportedIncident(t *testing.T, db *store.DB, tokens []string) int64 {
	t.Helper()
	id, _, err := db.InsertPending(-100123, 55, 7, 0, true, domain.Verdict{Action: domain.ActionQuarantine})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetIncidentState(id, domain.StateEvidenced); err != nil {
		t.Fatal(err)
	}
	if len(tokens) > 0 {
		if err := db.SaveIncidentTokens(id, tokens); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestButtonsOfferEnforceOnlyWhenNothingWasDone(t *testing.T) {
	if got := len(Buttons("k", false)); got != 2 {
		t.Fatalf("enforced incident got %d button rows, want 2", got)
	}
	rows := Buttons("k", true)
	if len(rows) != 3 {
		t.Fatalf("reported-only incident got %d button rows, want 3", len(rows))
	}
	act, key, ok := ParseCallback(rows[2][0].Data)
	if !ok || act != ActEnforce || key != "k" {
		t.Fatalf("third row must be the enforce button, got act=%q key=%q ok=%v", act, key, ok)
	}
}

func TestEnforceAppliesSanctionAndTrainsSpam(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	incidentID := reportedIncident(t, db, []string{"casino", "bonus"})

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true})

	var enforced []int64
	h.SetEnforcer(func(_ context.Context, inc store.IncidentRow) (bool, bool, error) {
		enforced = append(enforced, inc.ID)
		if len(inc.MessageIDs) != 1 || inc.MessageIDs[0] != 55 {
			t.Fatalf("enforcer got message ids %v, want the offending message [55]", inc.MessageIDs)
		}
		return true, true, nil
	})
	var trained []string
	h.SetTrainer(func(_ int64, label string, tokens []string) error {
		trained = append(trained, label+":"+strings.Join(tokens, " "))
		return nil
	})

	cb := Callback{ID: "cb", Data: encode(ActEnforce, strconv.FormatInt(incidentID, 10)), PresserID: 7}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if len(enforced) != 1 || enforced[0] != incidentID {
		t.Fatalf("enforcer calls = %v, want one for incident %d", enforced, incidentID)
	}
	if len(trained) != 1 || trained[0] != "spam:casino bonus" {
		t.Fatalf("training = %v, want the corpus taught from the stored tokens", trained)
	}
}

func TestEnforceRefusesAnIncidentThatAlreadyActed(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	incidentID := actedIncident(t, db, -100123, 55, 7, domain.ActionDeleteMute, nil)

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true})
	called := false
	h.SetEnforcer(func(context.Context, store.IncidentRow) (bool, bool, error) {
		called = true
		return true, true, nil
	})

	cb := Callback{ID: "cb", Data: encode(ActEnforce, strconv.FormatInt(incidentID, 10)), PresserID: 7}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("enforcing an already-enforced incident would re-mute a user another button may have freed")
	}
}

func TestEnforceIsNotAvailableToOutsiders(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	incidentID := reportedIncident(t, db, nil)

	f := fake.New()
	h := NewHandler(f, db, nil) // no operators, and the fake has no chat admins
	called := false
	h.SetEnforcer(func(context.Context, store.IncidentRow) (bool, bool, error) {
		called = true
		return true, true, nil
	})

	cb := Callback{ID: "cb", Data: encode(ActEnforce, strconv.FormatInt(incidentID, 10)), PresserID: 99}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("the most destructive button must be behind the same RBAC as the rest")
	}
}

// A failed enforcement must leave the button usable: the moderator saw the
// evidence and still has to be able to act after a transient Telegram error.
func TestEnforceFailureReleasesTheClaim(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	incidentID := reportedIncident(t, db, nil)

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true})
	attempts := 0
	h.SetEnforcer(func(context.Context, store.IncidentRow) (bool, bool, error) {
		attempts++
		if attempts == 1 {
			// Nothing landed: neither half succeeded.
			return false, false, context.DeadlineExceeded
		}
		return true, true, nil
	})

	cb := Callback{ID: "cb", Data: encode(ActEnforce, strconv.FormatInt(incidentID, 10)), PresserID: 7}
	if err := h.Handle(context.Background(), cb); err == nil {
		t.Fatal("a failed enforcement must surface as an error")
	}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("enforcer attempts = %d, want the retry to reach it", attempts)
	}
}

// The invariant every other branch of dispatch protects: a sanction that is
// live must be liftable. Enforcing from the button creates one, so undo has
// to keep working afterwards.
func TestUndoStillWorksAfterEnforcing(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	incidentID := reportedIncident(t, db, nil)

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true})
	h.SetEnforcer(func(_ context.Context, inc store.IncidentRow) (bool, bool, error) {
		// What the real wiring does after a successful sanction.
		if err := db.SetIncidentState(inc.ID, domain.StateCleaned); err != nil {
			return false, false, err
		}
		if err := db.MarkIncidentEnforced(inc.ID, domain.ActionDeleteMute); err != nil {
			return false, false, err
		}
		return true, true, nil
	})

	key := strconv.FormatInt(incidentID, 10)
	if err := h.Handle(context.Background(), Callback{ID: "c1", Data: encode(ActEnforce, key), PresserID: 7}); err != nil {
		t.Fatal(err)
	}
	row, err := db.GetIncident(incidentID)
	if err != nil {
		t.Fatal(err)
	}
	if row.DryRun {
		t.Fatal("an enforced incident must stop reading as dry-run, or the digest counts a live mute as an observation")
	}
	if !row.Sanctioned() {
		t.Fatalf("row must report an undoable sanction, got state=%q action=%q", row.State, row.Action)
	}

	// Now the moderator changes their mind.
	if err := h.Handle(context.Background(), Callback{ID: "c2", Data: encode(ActFalsePositive, key), PresserID: 7}); err != nil {
		t.Fatal(err)
	}
	if f.LastUnrestrict.UserID != 7 && f.LastUnban.UserID != 7 {
		t.Fatal("false positive after enforcement issued no Telegram undo call")
	}
}

// Pressing enforce twice must not sanction twice. The guard is the row, not
// the decision claim — the claim has to be free for the undo buttons.
func TestEnforceTwiceSanctionsOnce(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	incidentID := reportedIncident(t, db, nil)

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true})
	calls := 0
	h.SetEnforcer(func(_ context.Context, inc store.IncidentRow) (bool, bool, error) {
		calls++
		if err := db.SetIncidentState(inc.ID, domain.StateCleaned); err != nil {
			return false, false, err
		}
		return true, true, db.MarkIncidentEnforced(inc.ID, domain.ActionDeleteMute)
	})

	key := strconv.FormatInt(incidentID, 10)
	for i := 0; i < 2; i++ {
		if err := h.Handle(context.Background(), Callback{ID: "c", Data: encode(ActEnforce, key), PresserID: 7}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("enforcer ran %d times, want exactly 1", calls)
	}
}

func TestEnforceReplySaysWhatActuallyHappened(t *testing.T) {
	if got := enforceReply(true, true); !strings.Contains(got, "removed") {
		t.Fatalf("full success reply = %q", got)
	}
	if got := enforceReply(true, false); !strings.Contains(got, "STILL THERE") {
		t.Fatalf("a moderator told 'done' while the message stays is misled: %q", got)
	}
	if got := enforceReply(false, true); !strings.Contains(got, "FAILED") {
		t.Fatalf("a failed sanction must be stated: %q", got)
	}
}

// The row a button press acts on is loaded before the decision is claimed.
// A moderator's /spam that took the incident over and released its claim in
// between must not leave the press working from the stale copy: the enforce
// button would sanction a second time.
func TestEnforceRereadsTheIncidentAfterClaiming(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	incidentID := reportedIncident(t, db, nil)

	stale, err := db.GetIncident(incidentID)
	if err != nil || !stale.DryRun {
		t.Fatalf("precondition: stale row %+v err=%v", stale, err)
	}
	// The override, completed while the press was between load and claim.
	if claimed, _, _, err := db.ClaimManualOverride(incidentID); err != nil || !claimed {
		t.Fatalf("override claim=%v err=%v", claimed, err)
	}
	if err := db.FinishManualOverride(incidentID, domain.Verdict{Action: domain.ActionDeleteMute, Reason: "manual_spam"}, true); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(fake.New(), db, map[int64]bool{7: true})
	called := false
	h.SetEnforcer(func(context.Context, store.IncidentRow) (bool, bool, error) {
		called = true
		return true, true, nil
	})
	reply, err := h.dispatch(context.Background(), ActEnforce, stale, Callback{ID: "cb", PresserID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if called || reply != "already enforced" {
		t.Fatalf("enforce on a stale row: called=%v reply=%q, want no second sanction", called, reply)
	}
}
