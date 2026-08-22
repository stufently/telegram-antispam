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

// RelabelSample moves one already-recorded sample from one label to the
// other atomically: the old sample row and its token counts are removed and
// the new ones written in a single transaction.
//
// It exists because a moderator's correction is a RELABELING, not a second
// opinion. Training the opposite label on top of the first one would leave
// the corpus counting the same message as both spam and ham, which does not
// cancel out — it dilutes both classes and moves the decision boundary in a
// direction nobody chose. Callers use it for /ham after /spam (and the other
// way round).
//
// When no sample exists under `from`, this degrades to a plain RecordSample
// under `to`, so a first-time correction still trains. relabeled reports
// which of the two happened; added reports whether the `to` sample is new
// (false means it was already labeled that way and nothing was written).
func (db *DB) RelabelSample(scope, from, to, hash string, tokens []string) (relabeled, added bool, err error) {
	err = db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`DELETE FROM samples WHERE scope = ? AND label = ? AND normalized_hash = ?`,
			scope, from, hash,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		relabeled = n == 1
		if relabeled {
			if err := dropBayesTx(tx, scope, from, tokens); err != nil {
				return err
			}
		}

		ins, err := tx.Exec(
			`INSERT OR IGNORE INTO samples(scope, label, origin, normalized_hash) VALUES(?,?,?,?)`,
			scope, to, "user", hash,
		)
		if err != nil {
			return err
		}
		n, err = ins.RowsAffected()
		if err != nil {
			return err
		}
		added = n == 1
		if !added {
			return nil
		}
		return bumpBayesTx(tx, scope, to, tokens)
	})
	return relabeled, added, err
}
