package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOpenEnablesWAL(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.Read().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestWriteSerializesConcurrentWriters(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec("CREATE TABLE c(n INTEGER)")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	db.Write(func(tx *sql.Tx) error { _, e := tx.Exec("INSERT INTO c(n) VALUES(0)"); return e })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.Write(func(tx *sql.Tx) error {
				_, e := tx.Exec("UPDATE c SET n = n + 1")
				return e
			})
		}()
	}
	wg.Wait()
	var n int
	db.Read().QueryRow("SELECT n FROM c").Scan(&n)
	if n != 50 {
		t.Fatalf("n = %d, want 50 (writes lost to a race)", n)
	}
}

func TestWritePanicBecomesErrorNotCrash(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Write(func(tx *sql.Tx) error {
		panic("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic should become an error, got %v", err)
	}
	// the writer must still be alive and serving after a recovered panic
	if err := db.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec("CREATE TABLE ok(n INTEGER)")
		return e
	}); err != nil {
		t.Fatalf("writer dead after panic: %v", err)
	}
}

func TestWriteAfterCloseReturnsErrClosed(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(func(tx *sql.Tx) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close = %v, want ErrClosed", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close panicked or errored: %v", err)
	}
}
