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
