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
	for _, st := range []string{CaptchaNew, CaptchaChallenged, CaptchaFailing, CaptchaPassing, CaptchaPassed} {
		if row.State != st {
			if _, moved, err := db.TransitionCaptcha(-100, 7, 1, []string{row.State}, st); err != nil || !moved {
				t.Fatal(st, moved, err)
			}
		}
		again, started, err := db.BeginCaptcha(-100, 7, 99, 99)
		if err != nil || started || again.Attempt != 1 || again.State != st {
			t.Fatal(st, again, started, err)
		}
		if st == CaptchaPassing {
			known, err := db.CaptchaPassed(-100, 7)
			if err != nil || known {
				t.Fatal("passing is not passed", known, err)
			}
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
	prompt, err := db.SetCaptchaPrompt(-100, 8, 1, 55, 0, 80)
	if err != nil || prompt.EphemeralID != 55 || prompt.MessageID != 0 || prompt.State != CaptchaFailing || prompt.Deadline != 80 {
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

	if _, _, err = db.InsertPending(-100, 1, 7, 0, false, domain.Verdict{Action: domain.ActionMute}); err != nil {
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
	if _, _, err = db.InsertPending(-100, 2, 8, 0, true, domain.Verdict{Action: domain.ActionBan}); err != nil {
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
	if _, started, err = db.BeginCaptcha(-100, 11, 1, 1); err != nil || !started {
		t.Fatal(err)
	}
	if _, moved, err = db.TransitionCaptcha(-100, 11, 1, []string{CaptchaNew}, CaptchaPassing); err != nil || !moved {
		t.Fatal(err)
	}
	if err := db.RetryCaptcha(-100, 11, 1, 70); err != nil {
		t.Fatal(err)
	}
	again, found, err = db.GetCaptcha(-100, 11)
	if err != nil || !found || again.State != CaptchaPassing || again.Tries != 1 || again.Deadline != 70 {
		t.Fatal(again, found, err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.GetCaptcha(-100, 7); err != nil || !found {
		t.Fatal(found, err)
	}
}

func TestSanctionSinceOnlyMuteOrBan(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		user   int64
		action domain.Action
		dry    bool
		at     int64
		since  int64
		want   bool
	}{
		{1, domain.ActionDeleteMute, false, 1000, 1000, true},
		{2, domain.ActionMute, false, 1000, 1000, true},
		{3, domain.ActionBan, false, 1000, 1000, true},
		{4, domain.ActionQuarantine, false, 1000, 1000, false},
		{5, domain.ActionDeleteOnly, false, 1000, 1000, false},
		{6, domain.ActionNone, false, 1000, 1000, false},
		{7, domain.ActionBan, true, 1000, 1000, false},
		{8, domain.ActionMute, false, 999, 1000, false},
	}
	for _, tt := range cases {
		if _, _, err := db.InsertPending(-100, int(tt.user), tt.user, 0, tt.dry, domain.Verdict{Action: tt.action}); err != nil {
			t.Fatal(tt.user, err)
		}
		if err := setIncidentTime(db, tt.user, tt.at); err != nil {
			t.Fatal(tt.user, err)
		}
		ok, err := db.SanctionSince(-100, tt.user, tt.since)
		if err != nil || ok != tt.want {
			t.Fatalf("user %d action %s dry=%v at=%d since=%d: ok=%v err=%v want %v", tt.user, tt.action, tt.dry, tt.at, tt.since, ok, err, tt.want)
		}
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
