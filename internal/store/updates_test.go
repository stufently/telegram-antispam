package store

import (
	"path/filepath"
	"testing"
)

func TestMarkUpdateSeenDedup(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	fresh, err := db.MarkUpdateSeen(1001)
	if err != nil || !fresh {
		t.Fatalf("first: fresh=%v err=%v", fresh, err)
	}
	fresh, err = db.MarkUpdateSeen(1001)
	if err != nil || fresh {
		t.Fatalf("duplicate: fresh=%v err=%v (want fresh=false)", fresh, err)
	}
}
