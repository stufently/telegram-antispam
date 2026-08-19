package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/train"
)

// runImport parses CLI flags and imports samples from a file into the database.
// It accepts flags: --scope, --label (required), --origin, --db (defaults to DB_PATH env var).
// The remaining positional argument is the file path (required).
// openDB is an injected function to open the database, allowing tests to pass a temp DB.
func runImport(args []string, openDB func(string) (*store.DB, error)) (added, skipped int, err error) {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	scope := fs.String("scope", "global", "scope for training data")
	label := fs.String("label", "", "label (required): spam or ham")
	origin := fs.String("origin", "preset", "origin of the samples")
	dbPath := fs.String("db", os.Getenv("DB_PATH"), "path to database (default: DB_PATH env var)")

	if err := fs.Parse(args); err != nil {
		return 0, 0, err
	}

	// Validate label
	if *label != "spam" && *label != "ham" {
		return 0, 0, fmt.Errorf("invalid label %q: must be 'spam' or 'ham'", *label)
	}

	// Get positional argument (file path)
	if fs.NArg() < 1 {
		return 0, 0, fmt.Errorf("missing file argument")
	}
	filePath := fs.Arg(0)

	// Open database
	db, err := openDB(*dbPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Run migrations (idempotent)
	if err := db.Migrate(); err != nil {
		return 0, 0, fmt.Errorf("migrate: %w", err)
	}

	// Import file
	added, skipped, err = train.ImportFile(db, *scope, *label, *origin, filePath)
	if err != nil {
		return added, skipped, fmt.Errorf("import: %w", err)
	}

	return added, skipped, nil
}
