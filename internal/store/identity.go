package store

import "database/sql"

// UpsertIdentity records the current (username, displayName) for
// (chatID, userID) and returns the previously stored values along with
// whether they differ from the incoming ones. A brand-new row has no
// previous values to compare against, so prevUsername/prevDisplay are ""
// and changed is false. The SELECT and the upsert run in the same
// transaction so the read-then-write is atomic.
func (db *DB) UpsertIdentity(chatID, userID int64, username, displayName string) (prevUsername, prevDisplay string, changed bool, err error) {
	err = db.Write(func(tx *sql.Tx) error {
		existed := true
		selErr := tx.QueryRow(
			"SELECT username, display_name FROM user_identity WHERE chat_id=? AND user_id=?",
			chatID, userID,
		).Scan(&prevUsername, &prevDisplay)
		switch {
		case selErr == sql.ErrNoRows:
			existed = false
		case selErr != nil:
			return selErr
		}

		if existed {
			changed = prevUsername != username || prevDisplay != displayName
		}

		_, err := tx.Exec(`
INSERT INTO user_identity(chat_id, user_id, username, display_name, updated_at)
VALUES(?, ?, ?, ?, strftime('%s','now'))
ON CONFLICT(chat_id, user_id) DO UPDATE SET
	username = excluded.username,
	display_name = excluded.display_name,
	updated_at = excluded.updated_at`,
			chatID, userID, username, displayName)
		return err
	})
	return prevUsername, prevDisplay, changed, err
}
