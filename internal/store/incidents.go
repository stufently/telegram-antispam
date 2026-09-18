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

// ManualOverrideClaim is the decision value held on an incident while a
// moderator's /spam or /ban is being applied to it. It is deliberately the
// same value the admin chat's enforce button claims (admin.ActEnforce): both
// mean "a moderator is applying the sanction right now", so the enforce button
// pressed during an override answers "already decided: enforced" instead of
// sanctioning a second time.
const ManualOverrideClaim = "enf"

// ClaimManualOverride atomically takes over an existing incident for a
// moderator's manual verdict, if — and only if — that incident never applied
// a sanction. It reports whether the claim was taken and, when it was, every
// source message id of the incident (all parts of an album) and the admin-chat
// ids of its copied evidence, if any (the new card replies to them).
//
// "Never applied a sanction" is the whole point, and it is decided in the
// same conditional UPDATE that takes the claim, so a second /spam, or the
// admin chat's enforce button, cannot slip in between the check and the act:
//
//   - dry_run=1: the chat was observing, or the verdict was review-only;
//     nothing was done to the sender.
//   - state pending / evidenced / evidence_failed: the machine stopped
//     before the sanction landed (a failed evidence copy on a probabilistic
//     verdict, the production case; or a failed sanction or admin notice).
//     Enforce moves the state to acted the moment the sanction succeeds, so
//     none of these states can hide a live one. Moderator commands run on the
//     same per-chat sequencer as the automatic path, so none of them is an
//     incident still in flight either.
//   - the stored action never sanctions anything (quarantine / none).
//
// Everything else — an incident that did ban, mute or delete — is refused,
// as is one that already carries a moderator decision: a second sanction on
// top of the first would re-mute someone an admin may since have unmuted.
func (db *DB) ClaimManualOverride(id int64) (claimed bool, messageIDs, evidenceIDs []int, err error) {
	err = db.Write(func(tx *sql.Tx) error {
		res, execErr := tx.Exec(`
UPDATE incidents SET decision=?
WHERE id=? AND decision=''
  AND (dry_run=1
       OR state IN (?,?,?)
       OR NOT EXISTS (SELECT 1 FROM audit a WHERE a.incident_id=incidents.id
                      AND a.action IN (?,?,?,?)))`,
			ManualOverrideClaim, id,
			string(domain.StatePending), string(domain.StateEvidenced), string(domain.StateEvidenceFailed),
			string(domain.ActionBan), string(domain.ActionMute), string(domain.ActionDeleteMute), string(domain.ActionDeleteOnly),
		)
		if execErr != nil {
			return execErr
		}
		n, execErr := res.RowsAffected()
		if execErr != nil {
			return execErr
		}
		if n != 1 {
			return nil
		}
		claimed = true
		var (
			keyed int
			list  string
		)
		if execErr := tx.QueryRow("SELECT message_id, message_ids FROM incidents WHERE id=?", id).Scan(&keyed, &list); execErr != nil {
			return execErr
		}
		messageIDs = parseMessageIDs(list, keyed)
		rows, execErr := tx.Query("SELECT admin_message_id FROM evidence WHERE incident_id=? ORDER BY admin_message_id", id)
		if execErr != nil {
			return execErr
		}
		defer rows.Close()
		for rows.Next() {
			var mid int
			if execErr := rows.Scan(&mid); execErr != nil {
				return execErr
			}
			evidenceIDs = append(evidenceIDs, mid)
		}
		return rows.Err()
	})
	if err != nil {
		return false, nil, nil, err
	}
	return claimed, messageIDs, evidenceIDs, nil
}

// FinishManualOverride closes a claim taken by ClaimManualOverride.
//
// When the sanction landed, the incident is rewritten to say what actually
// happened, in one transaction with the release: state at least acted (the
// machine's own state write is separate and may have failed; without this an
// incident left in evidence_failed would be claimable again and sanctioned a
// second time), dry_run=0 (so the undo
// buttons, which refuse to lift a dry-run incident, can lift it, and so a
// later /spam is refused as already handled), and the audit row's action,
// scope, reason and signals replaced by the moderator's verdict (so the
// digest counts a real mute rather than the observation or the dropped
// probabilistic verdict it started as). When it did not land, only the claim
// is released, leaving the incident eligible for another try.
func (db *DB) FinishManualOverride(id int64, verdict domain.Verdict, sanctioned bool) error {
	signals, err := json.Marshal(verdict.Signals)
	if err != nil {
		return err
	}
	return db.Write(func(tx *sql.Tx) error {
		if sanctioned {
			if _, err := tx.Exec(`
UPDATE incidents SET dry_run=0,
  state=CASE WHEN state IN (?,?,?) THEN state ELSE ? END
WHERE id=?`,
				string(domain.StateActed), string(domain.StateCleaned), string(domain.StateDone), string(domain.StateActed), id,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				"UPDATE audit SET action=?, scope=?, reason=?, signals=? WHERE incident_id=?",
				string(verdict.Action), string(verdict.Scope), verdict.Reason, string(signals), id,
			); err != nil {
				return err
			}
		}
		_, err := tx.Exec("UPDATE incidents SET decision='' WHERE id=? AND decision=?", id, ManualOverrideClaim)
		return err
	})
}
