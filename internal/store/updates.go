package store

import "database/sql"

// MarkUpdateSeen records update_id. Returns fresh=true only the first time an
// id is seen; a duplicate returns fresh=false so the caller skips it.
func (db *DB) MarkUpdateSeen(updateID int64) (bool, error) {
	var fresh bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec("INSERT OR IGNORE INTO updates(update_id) VALUES(?)", updateID)
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

// PruneUpdates drops dedup rows older than the newest `keep` update ids and
// returns how many were removed.
//
// The table exists only so a redelivered update is not processed twice, and
// Telegram redelivers within minutes of an unacknowledged poll — yet every
// update ever seen stayed in it forever. Update ids increase monotonically
// per bot, so "older" is just arithmetic on the id, and no timestamp column
// (or migration) is needed. keep <= 0 is a no-op, so a misconfiguration
// cannot wipe the dedup window.
func (db *DB) PruneUpdates(keep int64) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	var n int64
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"DELETE FROM updates WHERE update_id <= (SELECT MAX(update_id) FROM updates) - ?",
			keep,
		)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}
