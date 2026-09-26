package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/stufently/telegram-antispam/internal/domain"
)

const (
	CaptchaNew        = "new"
	CaptchaChallenged = "challenged"
	CaptchaPassing    = "passing"
	CaptchaPassed     = "passed"
	CaptchaFailing    = "failing"
	CaptchaFailed     = "failed"
	CaptchaCancelled  = "cancelled"
)

type CaptchaRow struct {
	ChatID, UserID int64
	Attempt        int64
	State          string
	Deadline       int64
	FailAction     string
	Tries          int
	EphemeralID    int
	MessageID      int
	CreatedAt      int64
}

const captchaSelect = `SELECT chat_id, user_id, attempt, state, deadline, fail_action, tries, ephemeral_id, message_id, created_at FROM captcha_challenges`

type captchaScanner interface {
	Scan(dest ...any) error
}

func scanCaptcha(s captchaScanner) (CaptchaRow, bool, error) {
	var r CaptchaRow
	err := s.Scan(&r.ChatID, &r.UserID, &r.Attempt, &r.State, &r.Deadline, &r.FailAction, &r.Tries, &r.EphemeralID, &r.MessageID, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return CaptchaRow{}, false, nil
	}
	if err != nil {
		return CaptchaRow{}, false, err
	}
	return r, true, nil
}

func loadCaptchaTx(tx *sql.Tx, chatID, userID int64) (CaptchaRow, bool, error) {
	return scanCaptcha(tx.QueryRow(captchaSelect+` WHERE chat_id=? AND user_id=?`, chatID, userID))
}

func (db *DB) BeginCaptcha(chatID, userID, now, deadline int64) (CaptchaRow, bool, error) {
	var row CaptchaRow
	var started bool
	err := db.Write(func(tx *sql.Tx) error {
		existing, found, err := loadCaptchaTx(tx, chatID, userID)
		if err != nil {
			return err
		}
		if !found {
			row = CaptchaRow{ChatID: chatID, UserID: userID, Attempt: 1, State: CaptchaNew, Deadline: deadline, CreatedAt: now}
			_, err = tx.Exec(`INSERT INTO captcha_challenges(chat_id, user_id, attempt, state, deadline, fail_action, tries, ephemeral_id, message_id, created_at, updated_at)
				VALUES(?,?,?,?,?,'',0,0,0,?,?)`, chatID, userID, row.Attempt, row.State, deadline, now, now)
			started = err == nil
			return err
		}
		if existing.State != CaptchaFailed && existing.State != CaptchaCancelled {
			row = existing
			return nil
		}
		row = CaptchaRow{ChatID: chatID, UserID: userID, Attempt: existing.Attempt + 1, State: CaptchaNew, Deadline: deadline, CreatedAt: now}
		res, err := tx.Exec(`UPDATE captcha_challenges
			SET attempt=?, state=?, deadline=?, fail_action='', tries=0, ephemeral_id=0, message_id=0, created_at=?, updated_at=?
			WHERE chat_id=? AND user_id=? AND state IN (?,?)`,
			row.Attempt, CaptchaNew, deadline, now, now, chatID, userID, CaptchaFailed, CaptchaCancelled)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			row, _, err = loadCaptchaTx(tx, chatID, userID)
			return err
		}
		started = true
		return nil
	})
	return row, started, err
}

func (db *DB) CaptchaPassed(chatID, userID int64) (bool, error) {
	row, found, err := db.GetCaptcha(chatID, userID)
	if err != nil || !found {
		return false, err
	}
	return row.State == CaptchaPassed, nil
}

func (db *DB) GetCaptcha(chatID, userID int64) (CaptchaRow, bool, error) {
	return scanCaptcha(db.Read().QueryRow(captchaSelect+` WHERE chat_id=? AND user_id=?`, chatID, userID))
}

func (db *DB) TransitionCaptcha(chatID, userID, attempt int64, from []string, to string) (CaptchaRow, bool, error) {
	return db.updateCaptcha(chatID, userID, attempt, from, `state=?, updated_at=?`, []any{to, nowUnix()})
}

// MarkCaptchaPassing records the press and the moment it happened in one
// write. The deadline moves to now so a crash before the mute is lifted is
// due for the sweep immediately. The row stays passing until that lift lands.
func (db *DB) MarkCaptchaPassing(chatID, userID, attempt, deadline int64) (CaptchaRow, bool, error) {
	return db.updateCaptcha(chatID, userID, attempt, []string{CaptchaChallenged},
		`state=?, deadline=?, updated_at=?`, []any{CaptchaPassing, deadline, nowUnix()})
}

func (db *DB) FailCaptcha(chatID, userID, attempt int64, from []string, action string) (CaptchaRow, bool, error) {
	return db.updateCaptcha(chatID, userID, attempt, from,
		`state=?, fail_action=?, updated_at=?`, []any{CaptchaFailing, action, nowUnix()})
}

func (db *DB) updateCaptcha(chatID, userID, attempt int64, from []string, setSQL string, setArgs []any) (CaptchaRow, bool, error) {
	if len(from) == 0 {
		return CaptchaRow{}, false, fmt.Errorf("captcha transition: empty from")
	}
	ph := make([]string, len(from))
	args := append([]any{}, setArgs...)
	args = append(args, chatID, userID, attempt)
	for i, st := range from {
		ph[i] = "?"
		args = append(args, st)
	}
	q := `UPDATE captcha_challenges SET ` + setSQL + ` WHERE chat_id=? AND user_id=? AND attempt=? AND state IN (` + strings.Join(ph, ",") + `)`
	var row CaptchaRow
	var moved bool
	err := db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(q, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		moved = n == 1
		var found bool
		row, found, err = loadCaptchaTx(tx, chatID, userID)
		if err != nil {
			return err
		}
		if !found && moved {
			return fmt.Errorf("captcha transition: row disappeared")
		}
		return nil
	})
	return row, moved, err
}

func (db *DB) RetryCaptcha(chatID, userID, attempt, nextDeadline int64) error {
	return db.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE captcha_challenges SET tries=tries+1, deadline=?, updated_at=?
			WHERE chat_id=? AND user_id=? AND attempt=? AND state IN (?,?)`,
			nextDeadline, nowUnix(), chatID, userID, attempt, CaptchaFailing, CaptchaPassing)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("captcha retry: row chat=%d user=%d attempt=%d is not failing or passing", chatID, userID, attempt)
		}
		return nil
	})
}

func (db *DB) SetCaptchaPrompt(chatID, userID, attempt int64, ephemeralID, messageID int, deadline int64) (CaptchaRow, error) {
	var row CaptchaRow
	err := db.Write(func(tx *sql.Tx) error {
		// A press can commit `passing` (deadline = now) between the send and
		// this write. Pushing that deadline out to the full timeout would
		// hide a failed unmute from the sweep.
		res, err := tx.Exec(`UPDATE captcha_challenges
			SET ephemeral_id=?, message_id=?,
				deadline=CASE WHEN state=? THEN ? ELSE deadline END,
				updated_at=?
			WHERE chat_id=? AND user_id=? AND attempt=?`,
			ephemeralID, messageID, CaptchaChallenged, deadline, nowUnix(), chatID, userID, attempt)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("captcha prompt: no row chat=%d user=%d attempt=%d", chatID, userID, attempt)
		}
		loaded, found, err := loadCaptchaTx(tx, chatID, userID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("captcha prompt: row disappeared")
		}
		row = loaded
		return nil
	})
	return row, err
}

func (db *DB) DueCaptchas(now int64, limit int) ([]CaptchaRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := db.Read().Query(captchaSelect+`
		WHERE state IN (?,?,?,?) AND deadline<=?
		ORDER BY deadline, chat_id, user_id
		LIMIT ?`, CaptchaNew, CaptchaChallenged, CaptchaFailing, CaptchaPassing, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CaptchaRow
	for rows.Next() {
		row, _, err := scanCaptcha(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (db *DB) SanctionSince(chatID, userID, since int64) (bool, error) {
	var one int
	// A review, quarantine, delete-only or dry-run incident did not mute
	// this person. Only an audit action that is itself a mute or a ban —
	// written in the same transaction as the incident — means the captcha
	// must leave that mute alone.
	err := db.Read().QueryRow(`SELECT 1 FROM incidents i
		WHERE i.chat_id=? AND i.user_id=? AND i.dry_run=0 AND i.created_at>=?
		  AND EXISTS (
		    SELECT 1 FROM audit a
		    WHERE a.incident_id=i.id AND a.action IN (?,?,?)
		  )
		LIMIT 1`,
		chatID, userID, since,
		string(domain.ActionDeleteMute), string(domain.ActionMute), string(domain.ActionBan)).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func nowUnix() int64 {
	return timeNow().Unix()
}
