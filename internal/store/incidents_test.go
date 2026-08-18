package store

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestInsertPendingDedupAndAdvance(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	id, fresh, err := db.InsertPending(-100123, 55, 7, true)
	if err != nil || !fresh || id == 0 {
		t.Fatalf("insert: id=%d fresh=%v err=%v", id, fresh, err)
	}
	id2, fresh2, err := db.InsertPending(-100123, 55, 7, true)
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
}
