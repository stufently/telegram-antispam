package store

import "database/sql"

// InsertSample records a labeled sample for future learning (spec §8/§9).
// It is idempotent: (scope, label, normalized_hash) is unique, so a repeat
// write from a re-pressed admin button (or a repeat import) is silently
// ignored. It reports fresh=true only when the row was newly inserted, so
// callers can tell a first-time write from a no-op duplicate.
func (db *DB) InsertSample(scope, label, origin, normalizedHash string) (bool, error) {
	var fresh bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO samples(scope, label, origin, normalized_hash) VALUES(?,?,?,?)`,
			scope, label, origin, normalizedHash,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		fresh = n == 1
		return nil
	})
	return fresh, err
}
