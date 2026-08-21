package store

import (
	"database/sql"
	"encoding/json"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// InsertPending inserts a pending incident and its audit verdict atomically,
// keyed by (chat_id, message_id). On a duplicate it returns the existing id
// and fresh=false without adding a second audit row.
//
// The audit row is written here, at the pending stage — before the incident
// machine checks the dry-run gate and before any action is actually applied.
// Readers must therefore not treat an audit row as proof that its action
// happened; ActionCountsSince joins the incident's dry_run and state to tell
// applied actions from simulated and incomplete ones.
func (db *DB) InsertPending(chatID int64, messageID int, userID, senderChatID int64, dryRun bool, verdict domain.Verdict) (int64, bool, error) {
	var id int64
	var fresh bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
INSERT OR IGNORE INTO incidents(chat_id, message_id, user_id, sender_chat_id, state, dry_run)
VALUES(?,?,?,?,?,?)`,
			chatID, messageID, userID, senderChatID, string(domain.StatePending), b2i(dryRun))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			fresh = true
			id, err = res.LastInsertId()
			if err != nil {
				return err
			}
			signals, err := json.Marshal(verdict.Signals)
			if err != nil {
				return err
			}
			_, err = tx.Exec(
				`INSERT INTO audit(incident_id, action, scope, reason, signals) VALUES(?,?,?,?,?)`,
				id, string(verdict.Action), string(verdict.Scope), verdict.Reason, string(signals),
			)
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

// GetIncidentChat returns the source chat_id for an incident. It lets the
// admin callback handler resolve RBAC scope (which chat's admin list to
// check) from an opaque incident key without carrying the chat id on every
// callback payload.
func (db *DB) GetIncidentChat(id int64) (int64, error) {
	var chatID int64
	err := db.Read().QueryRow("SELECT chat_id FROM incidents WHERE id=?", id).Scan(&chatID)
	return chatID, err
}
