package store

// HasSpamIncident reports whether (chatID, userID) has at least one prior
// incident whose action was actually applied: dry_run = 0 and state has
// reached an "acted" milestone ('acted', 'cleaned', or 'done'). This is the
// M5 "known spammer" signal; a blocklist source can be OR-ed in later
// without changing callers.
//
// Incidents a moderator has since overturned ('fp') or lifted ('lift') do
// NOT count. A reversal only records a decision — the state stays 'done' —
// so without this filter a user cleared in the admin chat stayed a "known
// spammer" forever, and the reaction cleaner went on silently deleting
// every reaction they left in that chat. The sanction was undone; the
// consequences of the label were not.
func (db *DB) HasSpamIncident(chatID, userID int64) (bool, error) {
	var exists int
	err := db.Read().QueryRow(`
SELECT EXISTS(
	SELECT 1 FROM incidents
	WHERE chat_id = ? AND user_id = ?
	  AND dry_run = 0
	  AND state IN ('acted', 'cleaned', 'done')
	  AND COALESCE(decision, '') NOT IN ('fp', 'lift')
)`,
		chatID, userID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}
