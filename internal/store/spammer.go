package store

// HasSpamIncident reports whether (chatID, userID) has at least one prior
// incident whose action was actually applied: dry_run = 0 and state has
// reached an "acted" milestone ('acted', 'cleaned', or 'done'). This is the
// M5 "known spammer" signal; a blocklist source can be OR-ed in later
// without changing callers.
func (db *DB) HasSpamIncident(chatID, userID int64) (bool, error) {
	var exists int
	err := db.Read().QueryRow(`
SELECT EXISTS(
	SELECT 1 FROM incidents
	WHERE chat_id = ? AND user_id = ?
	  AND dry_run = 0
	  AND state IN ('acted', 'cleaned', 'done')
)`,
		chatID, userID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}
