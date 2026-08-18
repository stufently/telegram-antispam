package store

import "testing"

func TestUpsertIdentityNewRow(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	prevUsername, prevDisplay, changed, err := db.UpsertIdentity(1, 2, "a", "A")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("changed = true, want false for new row")
	}
	if prevUsername != "" || prevDisplay != "" {
		t.Fatalf("prevUsername=%q prevDisplay=%q, want empty for new row", prevUsername, prevDisplay)
	}
}

func TestUpsertIdentitySameValuesNotChanged(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if _, _, _, err := db.UpsertIdentity(1, 2, "a", "A"); err != nil {
		t.Fatal(err)
	}

	prevUsername, prevDisplay, changed, err := db.UpsertIdentity(1, 2, "a", "A")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("changed = true, want false for identical re-upsert")
	}
	if prevUsername != "a" || prevDisplay != "A" {
		t.Fatalf("prevUsername=%q prevDisplay=%q, want a/A", prevUsername, prevDisplay)
	}
}

func TestUpsertIdentityDetectsChange(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if _, _, _, err := db.UpsertIdentity(1, 2, "a", "A"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := db.UpsertIdentity(1, 2, "a", "A"); err != nil {
		t.Fatal(err)
	}

	prevUsername, prevDisplay, changed, err := db.UpsertIdentity(1, 2, "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatalf("changed = false, want true when username/display changed")
	}
	if prevUsername != "a" || prevDisplay != "A" {
		t.Fatalf("prevUsername=%q prevDisplay=%q, want a/A", prevUsername, prevDisplay)
	}
}
