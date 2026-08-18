package store

import (
	"database/sql"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// InsertPending inserts a pending incident keyed by (chat_id, message_id).
// On a duplicate it returns the existing id and fresh=false.
func (db *DB) InsertPending(chatID int64, messageID int, userID int64, dryRun bool) (int64, bool, error) {
	var id int64
	var fresh bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
INSERT OR IGNORE INTO incidents(chat_id, message_id, user_id, state, dry_run)
VALUES(?,?,?,?,?)`,
			chatID, messageID, userID, string(domain.StatePending), b2i(dryRun))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			fresh = true
			id, err = res.LastInsertId()
			return err
		}
		// existing row: fetch its id
		return tx.QueryRow(
			"SELECT id FROM incidents WHERE chat_id=? AND message_id=?", chatID, messageID,
		).Scan(&id)
	})
	return id, fresh, err
}

func (db *DB) SetIncidentState(id int64, state domain.IncidentState) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE incidents SET state=? WHERE id=?", string(state), id)
		return err
	})
}

func (db *DB) GetIncidentState(id int64) (domain.IncidentState, error) {
	var s string
	err := db.Read().QueryRow("SELECT state FROM incidents WHERE id=?", id).Scan(&s)
	return domain.IncidentState(s), err
}

func (db *DB) AddEvidence(incidentID int64, adminChatID int64, adminMessageIDs []int) error {
	return db.Write(func(tx *sql.Tx) error {
		for _, mid := range adminMessageIDs {
			if _, err := tx.Exec(
				"INSERT INTO evidence(incident_id, admin_chat_id, admin_message_id) VALUES(?,?,?)",
				incidentID, adminChatID, mid,
			); err != nil {
				return err
			}
		}
		return nil
	})
}
