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

// RecordSample inserts a labeled sample and, if it is new, bumps its tokens
// into the naive Bayes feature store — both in a single transaction. This
// closes the atomicity gap InsertSample+BumpBayes (called separately) would
// leave open: if the sample row committed but a later, separate bump
// failed, the row's existence would forever short-circuit re-import
// (InsertSample would keep returning fresh=false) while its tokens were
// never counted, silently losing that training data with no way to retry.
// Doing both writes on one tx means either both commit or neither does.
func (db *DB) RecordSample(scope, label, origin, hash string, tokens []string) (bool, error) {
	var added bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO samples(scope, label, origin, normalized_hash) VALUES(?,?,?,?)`,
			scope, label, origin, hash,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		added = n == 1
		if !added {
			return nil
		}
		return bumpBayesTx(tx, scope, label, tokens)
	})
	return added, err
}
