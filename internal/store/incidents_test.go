package store

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestInsertPendingDedupAndAdvance(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	verdict := domain.Verdict{
		Action: domain.ActionBan, Scope: domain.ScopeGlobal, Reason: "blocklist",
		Signals: []domain.Signal{{Name: "blocklist"}},
	}
	id, fresh, err := db.InsertPending(-100123, 55, 7, 0, true, verdict)
	if err != nil || !fresh || id == 0 {
		t.Fatalf("insert: id=%d fresh=%v err=%v", id, fresh, err)
	}
	id2, fresh2, err := db.InsertPending(-100123, 55, 7, 0, true, verdict)
	if err != nil || fresh2 || id2 != id {
		t.Fatalf("dup insert: id=%d fresh=%v err=%v (want same id, fresh=false)", id2, fresh2, err)
	}

	if err := db.SetIncidentState(id, domain.StateEvidenced); err != nil {
		t.Fatal(err)
	}
	st, err := db.GetIncidentState(id)
	if err != nil || st != domain.StateEvidenced {
		t.Fatalf("state=%v err=%v", st, err)
	}

	if err := db.AddEvidence(id, -1009999, []int{111, 112}); err != nil {
		t.Fatal(err)
	}
	var n int
	db.Read().QueryRow("SELECT COUNT(*) FROM evidence WHERE incident_id=?", id).Scan(&n)
	if n != 2 {
		t.Fatalf("evidence rows = %d, want 2", n)
	}

	var action, scope, reason, signals string
	if err := db.Read().QueryRow(
		"SELECT action, scope, reason, signals FROM audit WHERE incident_id=?", id,
	).Scan(&action, &scope, &reason, &signals); err != nil {
		t.Fatal(err)
	}
	if action != "ban" || scope != "global" || reason != "blocklist" || signals != `[{"Name":"blocklist","Detail":""}]` {
		t.Fatalf("unexpected audit row: action=%q scope=%q reason=%q signals=%q", action, scope, reason, signals)
	}
	var auditRows int
	if err := db.Read().QueryRow("SELECT COUNT(*) FROM audit WHERE incident_id=?", id).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows != 1 {
		t.Fatalf("duplicate InsertPending wrote %d audit rows, want 1", auditRows)
	}
}
