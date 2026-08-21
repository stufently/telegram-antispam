package store

import "fmt"

// BackupTo writes a consistent copy of the database to path using SQLite's
// VACUUM INTO.
//
// VACUUM INTO, not a file copy: the database runs in WAL mode, so the .db
// file on disk is only part of the state — a copy taken while the bot is
// running would miss everything still in the -wal file and could be torn
// mid-transaction. VACUUM INTO takes a read snapshot and writes a complete,
// already-compacted database, which is exactly what a backup should be.
//
// The destination must not exist; SQLite refuses to overwrite.
func (db *DB) BackupTo(path string) error {
	db.mu.RLock()
	closed := db.closed
	db.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	// Runs on the read pool on purpose: the source database is only read,
	// so this never contends with the single writer goroutine.
	if _, err := db.Read().Exec("VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("vacuum into %s: %w", path, err)
	}
	return nil
}
