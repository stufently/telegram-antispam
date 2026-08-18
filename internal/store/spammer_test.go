package store

import (
	"database/sql"
	"testing"
)

func insertIncident(t *testing.T, db *DB, chatID, messageID, userID int64, state string, dryRun int) {
	t.Helper()
	err := db.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec(
			"INSERT INTO incidents(chat_id, message_id, user_id, state, dry_run) VALUES(?, ?, ?, ?, ?)",
			chatID, messageID, userID, state, dryRun,
		)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHasSpamIncidentTrueForAppliedAction(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	insertIncident(t, db, 1, 10, 2, "acted", 0)

	has, err := db.HasSpamIncident(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("HasSpamIncident(1,2) = false, want true")
	}
}

func TestHasSpamIncidentFalseForUnknownUser(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	insertIncident(t, db, 1, 10, 2, "acted", 0)

	has, err := db.HasSpamIncident(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("HasSpamIncident(1,3) = true, want false (no row for user 3)")
	}
}

func TestHasSpamIncidentFalseForDryRun(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	insertIncident(t, db, 1, 11, 4, "acted", 1)

	has, err := db.HasSpamIncident(1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("HasSpamIncident(1,4) = true, want false (dry-run doesn't count)")
	}
}

func TestHasSpamIncidentFalseForNonAppliedState(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	insertIncident(t, db, 1, 12, 5, "pending", 0)

	has, err := db.HasSpamIncident(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("HasSpamIncident(1,5) = true, want false (pending is not an applied state)")
	}
}
