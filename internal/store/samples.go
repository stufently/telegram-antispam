package store

import "database/sql"

// InsertSample records a labeled sample for future learning (spec §8/§9).
// It is idempotent: (scope, label, normalized_hash) is unique, so a repeat
// write from a re-pressed admin button is silently ignored.
func (db *DB) InsertSample(scope, label, origin, normalizedHash string) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO samples(scope, label, origin, normalized_hash) VALUES(?,?,?,?)`,
			scope, label, origin, normalizedHash,
		)
		return err
	})
}
