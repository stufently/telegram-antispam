package admin

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// newMigrated builds a fresh, migrated store.DB backed by a temp file, for
// tests that need a real incident row (Handle looks up the incident's
// source chat via GetIncidentChat).
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

func TestHandleConfirmSpamTrainsBayesWhenAuthorized(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID, _, err := db.InsertPending(-100123, 55, 7, true, domain.Verdict{})
	if err != nil {
		t.Fatal(err)
	}

	f := fake.New()
	h := NewHandler(f, db, map[int64]bool{7: true}) // presser 7 is a global operator

	var calls []trainerCall
	h.SetTrainer(func(scope, label, text string) error {
		calls = append(calls, trainerCall{scope, label, text})
		return nil
	})

	cb := Callback{
		ID:           "cbid1",
		Data:         encode(ActConfirmSpam, strconv.FormatInt(incidentID, 10)),
		PresserID:    7,
		EvidenceText: "win a free casino bonus",
	}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 1 {
		t.Fatalf("trainer calls = %d, want 1 (calls=%v)", len(calls), calls)
	}
	want := trainerCall{"global", "spam", "win a free casino bonus"}
	if calls[0] != want {
		t.Fatalf("trainer call = %+v, want %+v", calls[0], want)
	}
}

func TestHandleConfirmSpamUnauthorizedDoesNotTrain(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	incidentID, _, err := db.InsertPending(-100123, 55, 7, true, domain.Verdict{})
	if err != nil {
		t.Fatal(err)
	}

	f := fake.New()
	f.Admins = nil                           // no chat admins, and presser is not a global operator
	h := NewHandler(f, db, map[int64]bool{}) // no global operators

	var calls []trainerCall
	h.SetTrainer(func(scope, label, text string) error {
		calls = append(calls, trainerCall{scope, label, text})
		return nil
	})

	cb := Callback{
		ID:           "cbid2",
		Data:         encode(ActConfirmSpam, strconv.FormatInt(incidentID, 10)),
		PresserID:    99, // unauthorized
		EvidenceText: "win a free casino bonus",
	}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 0 {
		t.Fatalf("trainer calls = %d, want 0 for unauthorized presser (calls=%v)", len(calls), calls)
	}
}
