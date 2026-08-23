package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/detect"
)

// Compile-time assertion that *DB satisfies detect.TrustSource. This lives
// in a _test.go file (not production code) so internal/store's non-test
// build never depends on internal/detect — only detect.TrustSource's shape
// is checked here, and wiring code elsewhere passes *store.DB where a
// detect.TrustSource is expected.
var _ detect.TrustSource = (*DB)(nil)

func TestBumpTrustIncrementsAndReturnsCount(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	count, err := db.BumpTrust(-100123, 555)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	count, err = db.BumpTrust(-100123, 555)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestBumpTrustIsPerChatUser(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if _, err := db.BumpTrust(-100123, 555); err != nil {
		t.Fatal(err)
	}
	count, err := db.BumpTrust(-100999, 555)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count for different chat = %d, want 1 (should not share counter)", count)
	}
}

func TestTrustCountReadsBackBumped(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if _, err := db.BumpTrust(-100123, 555); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BumpTrust(-100123, 555); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BumpTrust(-100123, 555); err != nil {
		t.Fatal(err)
	}

	count, err := db.TrustCount(-100123, 555)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestTrustCountDefaultsToZeroForUnknownUser(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	count, err := db.TrustCount(-100123, 999)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

// TestMigrateAddsMeaningfulCountToPreexistingUsersTable ensures Migrate can be
// run against a users table that predates the meaningful_count column (i.e.
// one created by an older schema without it) and backfills the column via a
// guarded ALTER TABLE, without erroring on a subsequent Migrate call.
func TestMigrateAddsMeaningfulCountToPreexistingUsersTable(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Simulate a pre-M3 DB: create a users table without meaningful_count.
	if err := db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE users (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			PRIMARY KEY(chat_id, user_id)
		)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate on pre-existing users table failed: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("second migrate failed: %v", err)
	}

	// meaningful_count must now be usable.
	count, err := db.BumpTrust(-100123, 555)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestBumpTrustDistinctIgnoresRepeats(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	const fp = "fingerprint-of-privet"
	count, bumped, err := db.BumpTrustDistinct(-100, 7, fp, 5)
	if err != nil || !bumped || count != 1 {
		t.Fatalf("first message: count=%d bumped=%v err=%v", count, bumped, err)
	}
	// The same message four more times: a spammer warming up an account.
	for i := 0; i < 4; i++ {
		count, bumped, err = db.BumpTrustDistinct(-100, 7, fp, 5)
		if err != nil {
			t.Fatal(err)
		}
		if bumped {
			t.Fatal("repeating one message must not earn trust")
		}
	}
	if count != 1 {
		t.Fatalf("count = %d after five identical messages, want 1", count)
	}

	// A different message does count.
	count, bumped, err = db.BumpTrustDistinct(-100, 7, "fingerprint-of-something-else", 5)
	if err != nil || !bumped || count != 2 {
		t.Fatalf("second distinct message: count=%d bumped=%v err=%v", count, bumped, err)
	}
}

func TestBumpTrustDistinctStopsRecordingOnceTrusted(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	for i := 0; i < 3; i++ {
		if _, _, err := db.BumpTrustDistinct(-100, 7, string(rune('a'+i)), 3); err != nil {
			t.Fatal(err)
		}
	}
	// At the limit: further messages must not grow the fingerprint table.
	if _, bumped, err := db.BumpTrustDistinct(-100, 7, "z", 3); err != nil || bumped {
		t.Fatalf("bumped=%v err=%v, want no further counting past the limit", bumped, err)
	}
	var n int
	if err := db.Read().QueryRow("SELECT COUNT(*) FROM trust_seen WHERE chat_id=-100 AND user_id=7").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("stored fingerprints = %d, want the limit of 3", n)
	}
}

func TestBumpTrustDistinctFallsBackWithoutText(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	// A media-only message has no text to fingerprint; it must not become a
	// duplicate of every other captionless message.
	for i := 0; i < 2; i++ {
		if _, bumped, err := db.BumpTrustDistinct(-100, 7, "", 5); err != nil || !bumped {
			t.Fatalf("bumped=%v err=%v", bumped, err)
		}
	}
}
