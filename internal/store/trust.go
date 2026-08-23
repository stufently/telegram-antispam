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

// BumpTrustDistinct increments the meaningful-message counter only when this
// (chat, user) has not sent the same message before, and reports whether it
// did. limit caps how many fingerprints are remembered per user: once the
// count reaches it the user is trusted anyway, so nothing more is recorded
// and the table cannot grow with a chatty member's whole history.
//
// It exists because the plain counter made warming up an account free: five
// copies of "привет" graduated a newcomer out of Bayes, the LLM and the
// fake-admin check. Requiring five DIFFERENT messages costs a spammer real
// effort while a real conversation clears it without noticing.
//
// A fingerprint that is empty (a media-only message has no text to
// fingerprint) falls back to the plain bump: such a message is already
// filtered by IsMeaningful, and inventing a shared constant for "no text"
// would make every captionless photo a duplicate of every other.
func (db *DB) BumpTrustDistinct(chatID, userID int64, fingerprint string, limit int) (count int, bumped bool, err error) {
	if fingerprint == "" {
		count, err = db.BumpTrust(chatID, userID)
		return count, err == nil, err
	}
	err = db.Write(func(tx *sql.Tx) error {
		if err := tx.QueryRow(
			"SELECT COALESCE(meaningful_count, 0) FROM users WHERE chat_id=? AND user_id=?",
			chatID, userID,
		).Scan(&count); err != nil && err != sql.ErrNoRows {
			return err
		}
		if limit > 0 && count >= limit {
			// Already trusted: counting further would only grow the table.
			return nil
		}
		res, err := tx.Exec(
			"INSERT OR IGNORE INTO trust_seen(chat_id, user_id, fingerprint) VALUES(?,?,?)",
			chatID, userID, fingerprint)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// Seen before: no credit for repeating yourself.
			return nil
		}
		bumped = true
		return tx.QueryRow(`
INSERT INTO users(chat_id, user_id, meaningful_count)
VALUES(?, ?, 1)
ON CONFLICT(chat_id, user_id) DO UPDATE SET
	meaningful_count = meaningful_count + 1
RETURNING meaningful_count`,
			chatID, userID).Scan(&count)
	})
	return count, bumped, err
}
