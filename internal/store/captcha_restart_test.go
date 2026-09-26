package store

import (
	"path/filepath"
	"testing"
)

func TestCaptchaRestartResetsPromptChat(t *testing.T) {
	for _, from := range []string{CaptchaCancelled, CaptchaFailed} {
		t.Run(from, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(); err != nil {
				t.Fatal(err)
			}
			if _, started, err := db.BeginCaptcha(-100, 7, 10, 40, "join_request"); err != nil || !started {
				t.Fatal(started, err)
			}
			if _, ok, err := db.TransitionCaptcha(-100, 7, 1, []string{CaptchaNew}, CaptchaChallenged); err != nil || !ok {
				t.Fatal(ok, err)
			}
			shown, err := db.SetCaptchaPrompt(-100, 7, 1, 700, 0, 11, 50)
			if err != nil || shown.PromptChatID != 700 || shown.Mode != "join_request" {
				t.Fatal(shown, err)
			}
			if _, ok, err := db.TransitionCaptcha(-100, 7, 1, []string{CaptchaChallenged}, from); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, started, err := db.BeginCaptcha(-100, 7, 20, 60, "button"); err != nil || !started {
				t.Fatal(started, err)
			}
			got, found, err := db.GetCaptcha(-100, 7)
			if err != nil || !found || got.PromptChatID != 0 || got.Mode != "button" || got.State != CaptchaNew || got.Attempt != 2 {
				t.Fatal(got, found, err)
			}
		})
	}
}
