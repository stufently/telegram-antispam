package store

import "database/sql"

// BumpTrust increments the meaningful-message counter for (chatID, userID)
// and returns the new count. The users row is created on first use.
func (db *DB) BumpTrust(chatID, userID int64) (count int, err error) {
	err = db.Write(func(tx *sql.Tx) error {
		return tx.QueryRow(`
INSERT INTO users(chat_id, user_id, meaningful_count)
VALUES(?, ?, 1)
ON CONFLICT(chat_id, user_id) DO UPDATE SET
	meaningful_count = meaningful_count + 1
RETURNING meaningful_count`,
			chatID, userID).Scan(&count)
	})
	return count, err
}

// TrustCount returns the current meaningful-message count for
// (chatID, userID), or 0 if no row exists yet.
func (db *DB) TrustCount(chatID, userID int64) (int, error) {
	var count int
	err := db.Read().QueryRow(
		"SELECT meaningful_count FROM users WHERE chat_id=? AND user_id=?",
		chatID, userID,
	).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}
