package store

import (
	"path/filepath"
	"testing"
)

func newMigrated(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestChatUpsertGetDisable(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	if err := db.UpsertChat(ChatRow{ChatID: -100123, Enabled: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	row, ok, err := db.GetChat(-100123)
	if err != nil || !ok || !row.DryRun {
		t.Fatalf("get: row=%+v ok=%v err=%v", row, ok, err)
	}
	if err := db.DisableChat(-100123); err != nil {
		t.Fatal(err)
	}
	row, _, _ = db.GetChat(-100123)
	if row.Enabled {
		t.Fatal("chat should be disabled")
	}
}

func TestResolveAlias(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	if err := db.AddAlias(-100, -200); err != nil {
		t.Fatal(err)
	}
	if got := db.ResolveChat(-100); got != -200 {
		t.Fatalf("resolve = %d, want -200", got)
	}
	if got := db.ResolveChat(-999); got != -999 {
		t.Fatalf("resolve unknown = %d, want -999", got)
	}
}
