package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/store"
)

func TestRunImport(t *testing.T) {
	// Create a temporary directory and database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a temporary file with 3 spam lines
	dataFile := filepath.Join(tmpDir, "data.txt")
	content := "spam line 1\nspam line 2\nspam line 3\n"
	if err := os.WriteFile(dataFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test data: %v", err)
	}

	// openDB always uses the same dbPath
	openDB := func(path string) (*store.DB, error) {
		return store.Open(dbPath)
	}

	// Test 1: First import should add 3 lines
	args := []string{"--scope", "global", "--label", "spam", "--origin", "preset", "--db", dbPath, dataFile}
	added, skipped, err := runImport(args, openDB)
	if err != nil {
		t.Fatalf("first import failed: %v", err)
	}
	if added != 3 {
		t.Errorf("first import: expected added=3, got %d", added)
	}
	if skipped != 0 {
		t.Errorf("first import: expected skipped=0, got %d", skipped)
	}

	// Test 2: Second import of the same file should skip all 3 lines
	args = []string{"--scope", "global", "--label", "spam", "--origin", "preset", "--db", dbPath, dataFile}
	added, skipped, err = runImport(args, openDB)
	if err != nil {
		t.Fatalf("second import failed: %v", err)
	}
	if added != 0 {
		t.Errorf("second import: expected added=0, got %d", added)
	}
	if skipped != 3 {
		t.Errorf("second import: expected skipped=3, got %d", skipped)
	}
}

func TestRunImportMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	openDB := func(path string) (*store.DB, error) {
		return store.Open(filepath.Join(tmpDir, "test.db"))
	}

	args := []string{"--label", "spam"}
	_, _, err := runImport(args, openDB)
	if err == nil {
		t.Error("expected error for missing file argument")
	}
}

func TestRunImportBadLabel(t *testing.T) {
	tmpDir := t.TempDir()
	dataFile := filepath.Join(tmpDir, "data.txt")
	os.WriteFile(dataFile, []byte("test"), 0644)

	openDB := func(path string) (*store.DB, error) {
		return store.Open(filepath.Join(tmpDir, "test.db"))
	}

	args := []string{"--label", "invalid", dataFile}
	_, _, err := runImport(args, openDB)
	if err == nil {
		t.Error("expected error for invalid label")
	}
}
