package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// IncidentRow is the slice of an incident the admin-chat undo path needs:
// who was sanctioned, in which chat, whether the sanction was real
// (dry_run=0), and how far the machine got.
type IncidentRow struct {
	ID     int64
	ChatID int64
	UserID int64
	DryRun bool
	State  domain.IncidentState
	Action domain.Action // from the audit row; "" when absent
}

// Sanctioned reports whether this incident actually applied a sanction that
// can be undone. It is deliberately strict: a dry-run incident changed
// nothing, and a state before StateActed means applyAction never ran (or
// failed), so pressing undo must not issue a pointless Telegram call.
func (r IncidentRow) Sanctioned() bool {
	if r.DryRun {
		return false
	}
	switch r.State {
	case domain.StateActed, domain.StateCleaned, domain.StateDone:
	default:
		return false
	}
	switch r.Action {
	case domain.ActionBan, domain.ActionMute, domain.ActionDeleteMute:
		return true
	default:
		return false
	}
}

// RecordDecision claims an incident's one-and-only moderator decision,
// reporting whether this call is the one that claimed it (and, if not, which
// decision already stands).
//
// The guard exists because the admin-chat buttons live forever in the chat
// history: without it, pressing "False positive" on a months-old evidence
// message would issue a fresh unban today — potentially lifting a LATER,
// unrelated sanction against the same user, since Telegram has no way to
// scope an unban to the incident that caused it. One decision per incident
// bounds that to a single press. The conditional UPDATE is the whole
// mechanism, so two admins pressing at once cannot both win.
func (db *DB) RecordDecision(incidentID int64, decision string) (claimed bool, existing string, err error) {
	err = db.Write(func(tx *sql.Tx) error {
		res, execErr := tx.Exec(
			"UPDATE incidents SET decision=? WHERE id=? AND decision=''",
			decision, incidentID,
		)
		if execErr != nil {
			return execErr
		}
		n, execErr := res.RowsAffected()
		if execErr != nil {
			return execErr
		}
		if n == 1 {
			claimed = true
			return nil
		}
		return tx.QueryRow("SELECT decision FROM incidents WHERE id=?", incidentID).Scan(&existing)
	})
	return claimed, existing, err
}

// GetIncident reads one incident joined with its audit action. The audit row
// is written in the same transaction as the incident, so a missing action is
// a corrupted row rather than a normal state; it is surfaced as an empty
// Action (never an error) so undo degrades to "nothing to lift" instead of
// failing the whole callback.
func (db *DB) GetIncident(id int64) (IncidentRow, error) {
	var (
		r      IncidentRow
		dry    int
		state  string
		action sql.NullString
	)
	err := db.Read().QueryRow(`
SELECT i.id, i.chat_id, i.user_id, i.dry_run, i.state, a.action
FROM incidents i LEFT JOIN audit a ON a.incident_id = i.id
WHERE i.id = ?`, id).Scan(&r.ID, &r.ChatID, &r.UserID, &dry, &state, &action)
	if err != nil {
		return IncidentRow{}, err
	}
	r.DryRun = dry == 1
	r.State = domain.IncidentState(state)
	if action.Valid {
		r.Action = domain.Action(action.String)
	}
	return r, nil
}

// ListEvidence returns the admin-chat messages copied for an incident, so
// "Delete evidence" can remove exactly what the bot posted. All rows for one
// incident share an admin chat (the machine writes them in one call), so a
// single chat id is returned alongside the message ids.
func (db *DB) ListEvidence(incidentID int64) (adminChatID int64, messageIDs []int, err error) {
	rows, err := db.Read().Query(
		"SELECT admin_chat_id, admin_message_id FROM evidence WHERE incident_id=? ORDER BY admin_message_id",
		incidentID,
	)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var chat int64
		var mid int
		if err := rows.Scan(&chat, &mid); err != nil {
			return 0, nil, err
		}
		adminChatID = chat
		messageIDs = append(messageIDs, mid)
	}
	return adminChatID, messageIDs, rows.Err()
}

// DeleteEvidenceRows drops an incident's evidence bookkeeping after the
// copies have been deleted from the admin chat, so a second press cannot
// re-issue deletes for message ids Telegram no longer knows.
func (db *DB) DeleteEvidenceRows(incidentID int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM evidence WHERE incident_id=?", incidentID)
		return err
	})
}

// tokenSep joins an incident's tokens for storage. Tokenize never emits
// whitespace inside a token, so a space is an unambiguous separator.
const tokenSep = " "

// SaveIncidentTokens stores the normalized tokens of the offending message so
// an admin's later "Confirm spam" / "False positive" press can train Bayes.
//
// Privacy: this is deliberately NOT the raw message text. Tokens are the
// normalized, de-obfuscated words the Bayes stage already accumulates counts
// for; nothing is kept that the feature store would not learn anyway, no
// links/formatting/media survive, and the row is deleted as soon as the
// incident is reviewed (see DeleteIncidentTokens) or aged out
// (PruneIncidentTokens). An empty token list writes nothing.
func (db *DB) SaveIncidentTokens(incidentID int64, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"INSERT OR REPLACE INTO incident_tokens(incident_id, tokens) VALUES(?,?)",
			incidentID, strings.Join(tokens, tokenSep),
		)
		return err
	})
}

// GetIncidentTokens returns the stored tokens for an incident. A missing row
// is not an error: it means the incident predates token capture, carried no
// text, or was already reviewed — the caller simply skips training.
func (db *DB) GetIncidentTokens(incidentID int64) ([]string, bool, error) {
	var joined string
	err := db.Read().QueryRow("SELECT tokens FROM incident_tokens WHERE incident_id=?", incidentID).Scan(&joined)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if joined == "" {
		return nil, false, nil
	}
	return strings.Split(joined, tokenSep), true, nil
}

// DeleteIncidentTokens drops an incident's captured tokens once it has been
// reviewed. Every admin button ends the review, so this keeps retention tied
// to moderator activity rather than to a fixed window.
func (db *DB) DeleteIncidentTokens(incidentID int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM incident_tokens WHERE incident_id=?", incidentID)
		return err
	})
}

// PruneIncidentTokens deletes captured tokens older than maxAge and returns
// how many rows were removed. It is the backstop for incidents nobody ever
// pressed a button on: without it, an unreviewed queue would retain message
// content indefinitely. A non-positive maxAge prunes nothing.
func (db *DB) PruneIncidentTokens(maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, nil
	}
	var n int64
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"DELETE FROM incident_tokens WHERE created_at < strftime('%s','now') - ?",
			int64(maxAge.Seconds()),
		)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}
