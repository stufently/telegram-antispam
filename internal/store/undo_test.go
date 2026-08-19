package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func newUndoDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestGetIncidentCarriesAuditAction(t *testing.T) {
	db := newUndoDB(t)
	id, _, err := db.InsertPending(-100, 5, 7, false, domain.Verdict{Action: domain.ActionBan})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetIncidentState(id, domain.StateDone); err != nil {
		t.Fatal(err)
	}
	row, err := db.GetIncident(id)
	if err != nil {
		t.Fatal(err)
	}
	if row.ChatID != -100 || row.UserID != 7 || row.DryRun {
		t.Fatalf("row = %+v", row)
	}
	if row.Action != domain.ActionBan {
		t.Fatalf("action = %q, want ban", row.Action)
	}
	if !row.Sanctioned() {
		t.Fatal("a live, completed ban must be reported as sanctioned")
	}
}

func TestSanctionedRejectsNonAppliedIncidents(t *testing.T) {
	cases := []struct {
		name string
		row  IncidentRow
	}{
		{"dry run", IncidentRow{DryRun: true, State: domain.StateDone, Action: domain.ActionBan}},
		{"never acted", IncidentRow{State: domain.StateEvidenced, Action: domain.ActionBan}},
		{"delete only", IncidentRow{State: domain.StateDone, Action: domain.ActionDeleteOnly}},
		{"no audit action", IncidentRow{State: domain.StateDone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.row.Sanctioned() {
				t.Fatalf("%+v must not be reported as sanctioned", tc.row)
			}
		})
	}
}

func TestIncidentTokensRoundTripAndDelete(t *testing.T) {
	db := newUndoDB(t)
	id, _, err := db.InsertPending(-100, 5, 7, false, domain.Verdict{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIncidentTokens(id, []string{"free", "casino"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetIncidentTokens(id)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if len(got) != 2 || got[0] != "free" || got[1] != "casino" {
		t.Fatalf("tokens = %v", got)
	}
	if err := db.DeleteIncidentTokens(id); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.GetIncidentTokens(id); ok {
		t.Fatal("tokens survived delete")
	}
}

func TestSaveIncidentTokensIgnoresEmptyAndMissingIsNotAnError(t *testing.T) {
	db := newUndoDB(t)
	if err := db.SaveIncidentTokens(1, nil); err != nil {
		t.Fatalf("empty save must be a no-op, got %v", err)
	}
	tokens, ok, err := db.GetIncidentTokens(1)
	if err != nil {
		t.Fatalf("missing row must not error, got %v", err)
	}
	if ok || tokens != nil {
		t.Fatalf("missing row returned ok=%v tokens=%v", ok, tokens)
	}
}

func TestPruneIncidentTokensDropsOnlyAgedRows(t *testing.T) {
	db := newUndoDB(t)
	fresh, _, err := db.InsertPending(-100, 1, 7, false, domain.Verdict{})
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := db.InsertPending(-100, 2, 7, false, domain.Verdict{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIncidentTokens(fresh, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIncidentTokens(old, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	// Backdate one row by two days so a one-day retention must take it.
	if err := db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE incident_tokens SET created_at = strftime('%s','now') - 172800 WHERE incident_id=?", old)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	n, err := db.PruneIncidentTokens(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}
	if _, ok, _ := db.GetIncidentTokens(fresh); !ok {
		t.Fatal("prune took the fresh row")
	}
	if _, ok, _ := db.GetIncidentTokens(old); ok {
		t.Fatal("prune left the aged row")
	}

	// A non-positive retention must be inert, not a table wipe.
	if n, err := db.PruneIncidentTokens(0); err != nil || n != 0 {
		t.Fatalf("prune(0) = %d, %v; want 0, nil", n, err)
	}
	if _, ok, _ := db.GetIncidentTokens(fresh); !ok {
		t.Fatal("prune(0) deleted rows")
	}
}

func TestListAndDeleteEvidence(t *testing.T) {
	db := newUndoDB(t)
	id, _, err := db.InsertPending(-100, 5, 7, false, domain.Verdict{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddEvidence(id, -900, []int{12, 11}); err != nil {
		t.Fatal(err)
	}
	chat, ids, err := db.ListEvidence(id)
	if err != nil {
		t.Fatal(err)
	}
	if chat != -900 || len(ids) != 2 || ids[0] != 11 || ids[1] != 12 {
		t.Fatalf("chat=%d ids=%v, want -900 [11 12]", chat, ids)
	}
	if err := db.DeleteEvidenceRows(id); err != nil {
		t.Fatal(err)
	}
	if _, ids, _ := db.ListEvidence(id); len(ids) != 0 {
		t.Fatalf("evidence rows survived delete: %v", ids)
	}
}
