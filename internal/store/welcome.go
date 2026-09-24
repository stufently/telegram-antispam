package store

import "database/sql"

// WasWelcomed reports whether this chat has already been sent a greeting
// for userID. The row stores only the pair and a timestamp — never the
// message text or the person's name.
func (db *DB) WasWelcomed(chatID, userID int64) (bool, error) {
	var one int
	err := db.Read().QueryRow(
		`SELECT 1 FROM welcome_sent WHERE chat_id=? AND user_id=?`,
		chatID, userID,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkWelcomed records that chatID/userID has been greeted. A repeat is
// not an error: the greeting is once per pair, and a second mark is a no-op.
func (db *DB) MarkWelcomed(chatID, userID int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO welcome_sent(chat_id, user_id) VALUES(?, ?)`,
			chatID, userID,
		)
		return err
	})
}
