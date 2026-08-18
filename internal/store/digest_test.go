package store

import (
	"database/sql"
	"testing"
)

func insertAuditAt(t *testing.T, db *DB, action string, createdAt int64) {
	t.Helper()
	if err := db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"INSERT INTO audit(incident_id, action, scope, reason, signals, created_at) VALUES(?,?,?,?,?,?)",
			1, action, "chat", "test", "{}", createdAt,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActionCountsSinceOnlyCountsInWindowRows(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	const now int64 = 1000000000
	since := now - 86400

	// In-window rows.
	insertAuditAt(t, db, "ban", now-10)
	insertAuditAt(t, db, "ban", now-20)
	insertAuditAt(t, db, "mute", now-30)

	// Out-of-window rows (older than the 24h cutoff).
	insertAuditAt(t, db, "ban", now-100000)
	insertAuditAt(t, db, "delete_mute", now-200000)

	counts, err := db.ActionCountsSince(since)
	if err != nil {
		t.Fatal(err)
	}

	if counts["ban"] != 2 {
		t.Fatalf("counts[ban] = %d, want 2", counts["ban"])
	}
	if counts["mute"] != 1 {
		t.Fatalf("counts[mute] = %d, want 1", counts["mute"])
	}
	if _, ok := counts["delete_mute"]; ok {
		t.Fatalf("counts[delete_mute] should be absent, got %d", counts["delete_mute"])
	}
	if len(counts) != 2 {
		t.Fatalf("len(counts) = %d, want 2 (%v)", len(counts), counts)
	}
}

func TestActionCountsSinceEmptyWhenNoRowsInWindow(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	const now int64 = 1000000000
	since := now - 86400

	insertAuditAt(t, db, "ban", now-100000)

	counts, err := db.ActionCountsSince(since)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("len(counts) = %d, want 0 (%v)", len(counts), counts)
	}
}
