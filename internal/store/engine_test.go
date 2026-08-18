package store

import (
	"database/sql"
	"path/filepath"
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
