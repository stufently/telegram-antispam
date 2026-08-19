package store

import (
	"database/sql"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// insertIncidentWithAudit writes the incident + audit pair the digest joins
// on, at a chosen audit timestamp, dry-run flag, and terminal state. It goes
// through raw SQL rather than InsertPending so a test can place a row in the
// past and in any state.
func insertIncidentWithAudit(t *testing.T, db *DB, action string, createdAt int64, dryRun bool, state domain.IncidentState) {
	t.Helper()
	dry := 0
	if dryRun {
		dry = 1
	}
	if err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"INSERT INTO incidents(chat_id, message_id, user_id, state, dry_run) VALUES(?,?,?,?,?)",
			-100, nextMessageID(), 7, string(state), dry,
		)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.Exec(
			"INSERT INTO audit(incident_id, action, scope, reason, signals, created_at) VALUES(?,?,?,?,?,?)",
			id, action, "chat", "test", "{}", createdAt,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

var messageIDSeq int

func nextMessageID() int {
	messageIDSeq++
	return messageIDSeq
}

// insertAuditAt writes an applied (live, done) incident+audit pair.
func insertAuditAt(t *testing.T, db *DB, action string, createdAt int64) {
	t.Helper()
	insertIncidentWithAudit(t, db, action, createdAt, false, domain.StateDone)
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

	counts, dryRun, incomplete, err := db.ActionCountsSince(since)
	if err != nil {
		t.Fatal(err)
	}
	if len(dryRun) != 0 || len(incomplete) != 0 {
		t.Fatalf("dryRun = %v, incomplete = %v, want both empty", dryRun, incomplete)
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

	counts, dryRun, incomplete, err := db.ActionCountsSince(since)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 || len(dryRun) != 0 || len(incomplete) != 0 {
		t.Fatalf("counts=%v dryRun=%v incomplete=%v, want all empty", counts, dryRun, incomplete)
	}
}

// The audit row is written at the pending stage, before the dry-run gate and
// before anything is applied. Simulated actions must therefore come back
// separately from real ones — a digest that merged them would report bans
// that never happened.
func TestActionCountsSinceSeparatesDryRunRows(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	const now int64 = 1000000000
	since := now - 86400

	insertIncidentWithAudit(t, db, "ban", now-10, false, domain.StateDone)
	insertIncidentWithAudit(t, db, "ban", now-20, true, domain.StateDone)
	insertIncidentWithAudit(t, db, "ban", now-30, true, domain.StateDone)
	insertIncidentWithAudit(t, db, "mute", now-40, true, domain.StateDone)

	applied, dryRun, incomplete, err := db.ActionCountsSince(since)
	if err != nil {
		t.Fatal(err)
	}
	if applied["ban"] != 1 || len(applied) != 1 {
		t.Fatalf("applied = %v, want only ban 1", applied)
	}
	if dryRun["ban"] != 2 || dryRun["mute"] != 1 || len(dryRun) != 2 {
		t.Fatalf("dryRun = %v, want ban 2 and mute 1", dryRun)
	}
	if len(incomplete) != 0 {
		t.Fatalf("incomplete = %v, want empty", incomplete)
	}
}

// A live incident whose evidence copy or sanction failed never acted, so its
// audit row must not be counted as an applied action either.
func TestActionCountsSinceSeparatesIncompleteLiveRows(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	const now int64 = 1000000000
	since := now - 86400

	insertIncidentWithAudit(t, db, "ban", now-10, false, domain.StateDone)
	insertIncidentWithAudit(t, db, "ban", now-20, false, domain.StatePending)
	insertIncidentWithAudit(t, db, "ban", now-30, false, domain.StateEvidenced)
	insertIncidentWithAudit(t, db, "kick", now-40, false, domain.StateEvidenceFailed)
	// Acted and cleaned both mean the sanction went through.
	insertIncidentWithAudit(t, db, "mute", now-50, false, domain.StateActed)
	insertIncidentWithAudit(t, db, "mute", now-60, false, domain.StateCleaned)

	applied, dryRun, incomplete, err := db.ActionCountsSince(since)
	if err != nil {
		t.Fatal(err)
	}
	if applied["ban"] != 1 || applied["mute"] != 2 || len(applied) != 2 {
		t.Fatalf("applied = %v, want ban 1 and mute 2", applied)
	}
	if incomplete["ban"] != 2 || incomplete["kick"] != 1 || len(incomplete) != 2 {
		t.Fatalf("incomplete = %v, want ban 2 and kick 1", incomplete)
	}
	if len(dryRun) != 0 {
		t.Fatalf("dryRun = %v, want empty", dryRun)
	}
}
