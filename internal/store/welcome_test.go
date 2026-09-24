package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestWelcomedRoundTrip(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	assertWelcomeRoundTrip(t, db)
	if err := db.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	welcomed, err := db.WasWelcomed(-100, 7)
	if err != nil || !welcomed {
		t.Fatalf("row lost across a second migrate: welcomed=%v err=%v", welcomed, err)
	}

	// A database created before welcome_sent existed. Migrate must add the
	// table without disturbing what is already there.
	old, err := Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := old.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE legacy_marker (x INTEGER)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := old.Migrate(); err != nil {
		t.Fatalf("migrate on a pre-existing database: %v", err)
	}
	if err := old.Migrate(); err != nil {
		t.Fatalf("second migrate on a pre-existing database: %v", err)
	}
	assertWelcomeRoundTrip(t, old)
}

func assertWelcomeRoundTrip(t *testing.T, db *DB) {
	t.Helper()
	welcomed, err := db.WasWelcomed(-100, 7)
	if err != nil {
		t.Fatal(err)
	}
	if welcomed {
		t.Fatal("empty table reported the user as already welcomed")
	}
	if err := db.MarkWelcomed(-100, 7); err != nil {
		t.Fatal(err)
	}
	welcomed, err = db.WasWelcomed(-100, 7)
	if err != nil || !welcomed {
		t.Fatalf("after mark: welcomed=%v err=%v", welcomed, err)
	}
	if err := db.MarkWelcomed(-100, 7); err != nil {
		t.Fatalf("repeat mark must not fail: %v", err)
	}
	welcomed, err = db.WasWelcomed(-100, 7)
	if err != nil || !welcomed {
		t.Fatalf("after repeat mark: welcomed=%v err=%v", welcomed, err)
	}
	otherUser, err := db.WasWelcomed(-100, 8)
	if err != nil || otherUser {
		t.Fatalf("other user in the same chat: welcomed=%v err=%v", otherUser, err)
	}
	otherChat, err := db.WasWelcomed(-200, 7)
	if err != nil || otherChat {
		t.Fatalf("same user in another chat: welcomed=%v err=%v", otherChat, err)
	}
}
