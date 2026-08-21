package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/stufently/telegram-antispam/internal/store"
)

// runBackup implements the `backup` subcommand: it writes a consistent copy
// of the database to a file, or to stdout when the destination is "-".
//
// Streaming to stdout is what makes this usable from CI. The runtime image
// is distroless: there is no shell, no tar and no sqlite3 binary in it, so
// neither `kubectl cp` (which shells out to tar inside the container) nor an
// in-container copy script can be used. `kubectl exec ... -- /tg-antispam
// backup - > dump.db` needs none of that — the bot's own binary is the only
// thing that has to exist.
func runBackup(args []string, open func(string) (*store.DB, error)) (err error) {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dbPath := fs.String("db", os.Getenv("DB_PATH"), "path to the database (default $DB_PATH)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return errors.New("no database path: pass -db or set DB_PATH")
	}
	dst := "-"
	if fs.NArg() > 0 {
		dst = fs.Arg(0)
	}

	db, err := open(*dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if dst != "-" {
		return db.BackupTo(dst)
	}

	// VACUUM INTO needs a real file, so the snapshot lands in a temp dir
	// first and is streamed out afterwards. /tmp is a writable emptyDir in
	// the pod precisely because SQLite needs somewhere to write.
	tmpDir, err := os.MkdirTemp("", "tg-antispam-backup")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	snapshot := filepath.Join(tmpDir, "snapshot.db")
	if err := db.BackupTo(snapshot); err != nil {
		return err
	}
	f, err := os.Open(snapshot)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// A short write here means a truncated backup that still LOOKS like a
	// file on the other end, so the error is returned rather than logged.
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return fmt.Errorf("stream snapshot: %w", err)
	}
	return nil
}
