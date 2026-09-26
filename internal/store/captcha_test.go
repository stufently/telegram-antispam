package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestCaptchaLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	row, started, err := db.BeginCaptcha(-100, 7, 10, 40)
	if err != nil || !started || row.Attempt != 1 || row.State != CaptchaNew || row.Deadline != 40 || row.CreatedAt != 10 {
		t.Fatal(row, started, err)
	}
	for _, st := range []string{CaptchaNew, CaptchaChallenged, CaptchaFailing, CaptchaPassed} {
		if row.State != st {
			if _, moved, err := db.TransitionCaptcha(-100, 7, 1, []string{row.State}, st); err != nil || !moved {
				t.Fatal(st, moved, err)
			}
		}
		again, started, err := db.BeginCaptcha(-100, 7, 99, 99)
		if err != nil || started || again.Attempt != 1 || again.State != st {
			t.Fatal(st, again, started, err)
		}
		row = again
	}
	if _, moved, err := db.TransitionCaptcha(-100, 7, 1, []string{CaptchaPassed}, CaptchaFailed); err != nil || !moved {
		t.Fatal(err)
	}
	restart, started, err := db.BeginCaptcha(-100, 7, 20, 50)
	if err != nil || !started || restart.Attempt != 2 || restart.State != CaptchaNew || restart.Tries != 0 || restart.FailAction != "" || restart.EphemeralID != 0 || restart.MessageID != 0 || restart.CreatedAt != 20 || restart.Deadline != 50 {
		t.Fatal(restart, started, err)
	}
	if _, moved, err := db.TransitionCaptcha(-100, 7, 2, []string{CaptchaNew}, CaptchaCancelled); err != nil || !moved {
		t.Fatal(err)
	}
	restart, started, err = db.BeginCaptcha(-100, 7, 30, 60)
	if err != nil || !started || restart.Attempt != 3 || restart.State != CaptchaNew {
		t.Fatal(restart, started, err)
	}
	moved, err := transitionOK(db, -100, 7, 3, []string{CaptchaNew}, CaptchaChallenged)
	if err != nil || !moved {
		t.Fatal(moved, err)
	}
	if _, moved, err = db.TransitionCaptcha(-100, 7, 99, []string{CaptchaChallenged}, CaptchaPassed); err != nil || moved {
		t.Fatal(moved, err)
	}
	if _, moved, err = db.TransitionCaptcha(-100, 7, 3, []string{CaptchaNew}, CaptchaPassed); err != nil || moved {
		t.Fatal(moved, err)
	}
	passed, moved, err := db.TransitionCaptcha(-100, 7, 3, []string{CaptchaChallenged}, CaptchaPassed)
	if err != nil || !moved || passed.State != CaptchaPassed {
		t.Fatal(passed, moved, err)
	}
	if _, moved, err = db.TransitionCaptcha(-100, 7, 3, []string{CaptchaChallenged}, CaptchaPassed); err != nil || moved {
		t.Fatal(moved, err)
	}
	ok, err := db.CaptchaPassed(-100, 7)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}

	if _, started, err = db.BeginCaptcha(-100, 8, 1, 15); err != nil || !started {
		t.Fatal(err)
	}
	failed, moved, err := db.FailCaptcha(-100, 8, 1, []string{CaptchaNew}, "kick")
	if err != nil || !moved || failed.State != CaptchaFailing || failed.FailAction != "kick" {
		t.Fatal(failed, moved, err)
	}
	if err := db.RetryCaptcha(-100, 8, 1, 80); err != nil {
		t.Fatal(err)
	}
	again, found, err := db.GetCaptcha(-100, 8)
	if err != nil || !found || again.Tries != 1 || again.Deadline != 80 || again.State != CaptchaFailing {
		t.Fatal(again, found, err)
	}
	prompt, err := db.SetCaptchaPrompt(-100, 8, 1, 55, 0)
	if err != nil || prompt.EphemeralID != 55 || prompt.MessageID != 0 || prompt.State != CaptchaFailing {
		t.Fatal(prompt, err)
	}

	if _, started, err = db.BeginCaptcha(-100, 2, 1, 10); err != nil || !started {
		t.Fatal(err)
	}
	if _, started, err = db.BeginCaptcha(-100, 5, 1, 30); err != nil || !started {
		t.Fatal(err)
	}
	if _, moved, err = db.TransitionCaptcha(-100, 5, 1, []string{CaptchaNew}, CaptchaChallenged); err != nil || !moved {
		t.Fatal(err)
	}
	if _, started, err = db.BeginCaptcha(-100, 6, 1, 40); err != nil || !started {
		t.Fatal(err)
	}
	if _, moved, err = db.FailCaptcha(-100, 6, 1, []string{CaptchaNew}, "unrestrict"); err != nil || !moved {
		t.Fatal(err)
	}
	if _, started, err = db.BeginCaptcha(-100, 3, 1, 5000); err != nil || !started {
		t.Fatal(err)
	}
	if _, started, err = db.BeginCaptcha(-100, 4, 1, 1); err != nil || !started {
		t.Fatal(err)
	}
	if _, moved, err = db.TransitionCaptcha(-100, 4, 1, []string{CaptchaNew}, CaptchaPassed); err != nil || !moved {
		t.Fatal(err)
	}
	due, err := db.DueCaptchas(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int64, len(due))
	for i, r := range due {
		got[i] = r.UserID
		if r.State == CaptchaPassed || r.State == CaptchaFailed || r.State == CaptchaCancelled || r.Deadline > 100 {
			t.Fatal(r)
		}
	}
	if len(got) != 4 || got[0] != 2 || got[1] != 5 || got[2] != 6 || got[3] != 8 {
		t.Fatal(got)
	}

	if _, _, err = db.InsertPending(-100, 1, 7, 0, false, domain.Verdict{}); err != nil {
		t.Fatal(err)
	}
	if err := setIncidentTime(db, 7, 1000); err != nil {
		t.Fatal(err)
	}
	ok, err = db.SanctionSince(-100, 7, 1000)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ok, err = db.SanctionSince(-100, 7, 1001)
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	if _, _, err = db.InsertPending(-100, 2, 8, 0, true, domain.Verdict{}); err != nil {
		t.Fatal(err)
	}
	if err := setIncidentTime(db, 8, 1000); err != nil {
		t.Fatal(err)
	}
	ok, err = db.SanctionSince(-100, 8, 1000)
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	ok, err = db.SanctionSince(-100, 9, 0)
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.GetCaptcha(-100, 7); err != nil || !found {
		t.Fatal(found, err)
	}
}

func transitionOK(db *DB, chat, user, attempt int64, from []string, to string) (bool, error) {
	_, moved, err := db.TransitionCaptcha(chat, user, attempt, from, to)
	return moved, err
}

func setIncidentTime(db *DB, user, at int64) error {
	return db.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE incidents SET created_at=? WHERE user_id=?`, at, user)
		return err
	})
}
