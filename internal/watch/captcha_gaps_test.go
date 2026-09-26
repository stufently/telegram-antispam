package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

// captchaSpy counts the store calls a press is not allowed to make, and can
// plant a sanction at the moment MarkCaptchaPassing runs.
type captchaSpy struct {
	*store.DB
	gets, marks, sanctions int
	onMark                 func()
}

func (s *captchaSpy) GetCaptcha(chatID, userID int64) (store.CaptchaRow, bool, error) {
	s.gets++
	return s.DB.GetCaptcha(chatID, userID)
}

func (s *captchaSpy) MarkCaptchaPassing(chatID, userID, attempt, deadline int64) (store.CaptchaRow, bool, error) {
	s.marks++
	if s.onMark != nil {
		s.onMark()
	}
	return s.DB.MarkCaptchaPassing(chatID, userID, attempt, deadline)
}

func (s *captchaSpy) SanctionSince(chatID, userID, since int64) (bool, error) {
	s.sanctions++
	return s.DB.SanctionSince(chatID, userID, since)
}

func (e *capEnv) countList() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.counts...)
}

func (e *capEnv) sanction(user int64, action domain.Action) {
	e.t.Helper()
	if _, _, err := e.db.InsertPending(capChat, int(user), user, 0, false, domain.Verdict{Action: action}); err != nil {
		e.t.Fatal(err)
	}
}

// seedDue inserts a row that Sweep will see immediately. tries is applied with
// RetryCaptcha so the deadline stays in the past.
func (e *capEnv) seedDue(mode, kind, action string, tries int) {
	e.t.Helper()
	deadline := e.clock.Unix() - 1
	row, started, err := e.db.BeginCaptcha(capChat, 7, e.clock.Unix(), deadline, mode)
	if err != nil || !started {
		e.t.Fatal(started, err)
	}
	switch kind {
	case store.CaptchaFailing:
		if _, ok, err := e.db.FailCaptcha(capChat, 7, row.Attempt, []string{store.CaptchaNew}, action); err != nil || !ok {
			e.t.Fatal(ok, err)
		}
	case store.CaptchaPassing:
		if _, ok, err := e.db.TransitionCaptcha(capChat, 7, row.Attempt, []string{store.CaptchaNew}, store.CaptchaChallenged); err != nil || !ok {
			e.t.Fatal(ok, err)
		}
		if _, ok, err := e.db.MarkCaptchaPassing(capChat, 7, row.Attempt, deadline); err != nil || !ok {
			e.t.Fatal(ok, err)
		}
	default:
		e.t.Fatalf("seed %s", kind)
	}
	for i := 0; i < tries; i++ {
		if err := e.db.RetryCaptcha(capChat, 7, row.Attempt, deadline); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *capEnv) sweep() {
	e.t.Helper()
	if _, err := e.c.Sweep(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func actionCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		switch c {
		case "BanMember", "UnbanMember", "UnrestrictMember", "ApproveJoinRequest", "DeclineJoinRequest":
			out = append(out, c)
		}
	}
	return out
}

func TestCaptchaPressExpiredByDeadlineWithoutSweep(t *testing.T) {
	for _, past := range []time.Duration{0, time.Second} {
		t.Run(past.String(), func(t *testing.T) {
			e := newCap(t)
			e.join(7, "Ada")
			row := e.row(7)
			e.clock = time.Unix(row.Deadline, 0).Add(past)
			if out := e.press(7, row.Attempt, 7); out != CaptchaExpired || e.row(7).State != store.CaptchaChallenged || called(e.calls(), "UnrestrictMember") {
				t.Fatal(out, e.row(7).State, e.calls())
			}
			if got := e.row(7); got.Attempt != row.Attempt || got.Deadline != row.Deadline {
				t.Fatal(got)
			}
		})
	}
}

func TestCaptchaPressSanctionChecks(t *testing.T) {
	t.Run("before press", func(t *testing.T) {
		e := newCap(t)
		e.join(7, "Ada")
		e.sanction(7, domain.ActionBan)
		spy := &captchaSpy{DB: e.db}
		e.c.Store = spy
		row := e.row(7)
		if out := e.press(7, row.Attempt, 7); out != CaptchaCancelledSanction || spy.marks != 0 || called(e.calls(), "UnrestrictMember") {
			t.Fatal(out, spy.marks, e.calls())
		}
		if got := e.row(7); got.State != store.CaptchaCancelled {
			t.Fatal(got.State)
		}
	})
	t.Run("during mark", func(t *testing.T) {
		e := newCap(t)
		e.join(7, "Ada")
		spy := &captchaSpy{DB: e.db}
		spy.onMark = func() { e.sanction(7, domain.ActionMute) }
		e.c.Store = spy
		row := e.row(7)
		if out := e.press(7, row.Attempt, 7); out != CaptchaCancelledSanction || spy.marks != 1 || called(e.calls(), "UnrestrictMember") {
			t.Fatal(out, spy.marks, e.calls())
		}
		if got := e.row(7); got.State != store.CaptchaCancelled {
			t.Fatal(got.State)
		}
	})
}

func TestCaptchaSweepChallengedSanctioned(t *testing.T) {
	for _, action := range []string{"kick", "keep_muted"} {
		t.Run(action, func(t *testing.T) {
			e := newCap(t)
			e.join(7, "Ada")
			e.sanction(7, domain.ActionBan)
			cfg := *e.live.Current()
			cfg.Captcha.OnFail = action
			e.live.Swap(&cfg)
			e.due()
			if e.row(7).State != store.CaptchaCancelled || !e.saw(string(CaptchaCancelledSanction)) {
				t.Fatal(e.row(7).State, e.countList())
			}
			if called(e.calls(), "BanMember") || called(e.calls(), "UnbanMember") || called(e.calls(), "UnrestrictMember") {
				t.Fatal(e.calls())
			}
			if e.port.LastDeleteEphemeral.EphemeralID != 55 {
				t.Fatal(e.port.LastDeleteEphemeral)
			}
		})
	}
}

func TestCaptchaGiveUpThresholds(t *testing.T) {
	cases := []struct {
		name, mode, kind, action, wantState, wantMetric, wantCall string
		tries                                                     int
	}{
		{"kick-5", "button", store.CaptchaFailing, "kick", store.CaptchaFailed, string(CaptchaGaveUp), "", 5},
		{"unrestrict-5", "button", store.CaptchaFailing, "unrestrict", store.CaptchaCancelled, string(CaptchaGaveUp), "", 5},
		{"passing-button-5", "button", store.CaptchaPassing, "", store.CaptchaCancelled, string(CaptchaGaveUp), "", 5},
		{"passing-join-5", "join_request", store.CaptchaPassing, "", store.CaptchaCancelled, string(CaptchaGaveUp), "", 5},
		{"decline-5", "join_request", store.CaptchaFailing, "decline", store.CaptchaFailed, string(CaptchaGaveUp), "", 5},
		{"kick-4", "button", store.CaptchaFailing, "kick", store.CaptchaFailed, string(CaptchaFailedKick), "BanMember", 4},
		{"unrestrict-4", "button", store.CaptchaFailing, "unrestrict", store.CaptchaCancelled, string(CaptchaReleased), "UnrestrictMember", 4},
		{"passing-button-4", "button", store.CaptchaPassing, "", store.CaptchaPassed, string(CaptchaPassed), "UnrestrictMember", 4},
		{"passing-join-4", "join_request", store.CaptchaPassing, "", store.CaptchaPassed, string(CaptchaApproved), "ApproveJoinRequest", 4},
		{"decline-4", "join_request", store.CaptchaFailing, "decline", store.CaptchaFailed, string(CaptchaFailedDecline), "DeclineJoinRequest", 4},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			e := newCap(t)
			e.seedDue(tt.mode, tt.kind, tt.action, tt.tries)
			e.sweep()
			got := e.countList()
			actions := actionCalls(e.calls())
			if e.row(7).State != tt.wantState || len(got) != 1 || got[0] != tt.wantMetric {
				t.Fatal(e.row(7).State, got, actions)
			}
			if tt.wantCall == "" {
				if len(actions) != 0 {
					t.Fatal(actions)
				}
				return
			}
			if !called(e.calls(), tt.wantCall) {
				t.Fatal(e.calls())
			}
		})
	}
}

func TestCaptchaSweepPassingSanctioned(t *testing.T) {
	e := newCap(t)
	e.seedDue("button", store.CaptchaPassing, "", 0)
	e.sanction(7, domain.ActionMute)
	e.sweep()
	if e.row(7).State != store.CaptchaCancelled || !e.saw(string(CaptchaCancelledSanction)) || called(e.calls(), "UnrestrictMember") {
		t.Fatal(e.row(7).State, e.countList(), e.calls())
	}
}

func TestCaptchaCallbackMalformedAttempt(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	before := e.row(7)
	spy := &captchaSpy{DB: e.db}
	e.c.Store = spy
	for _, attempt := range []int64{0, -1} {
		if out := e.press(7, attempt, 7); out != CaptchaInvalid {
			t.Fatal(attempt, out)
		}
	}
	if spy.gets != 0 {
		t.Fatal(spy.gets)
	}
	if got := e.row(7); got != before {
		t.Fatal(got, before)
	}
}

func TestCaptchaRestrictErrorCancels(t *testing.T) {
	e := newCap(t)
	e.port.RestrictErr = errors.New("denied")
	if out := e.join(7, "Ada"); out != CaptchaChallenged {
		t.Fatal(out)
	}
	if e.row(7).State != store.CaptchaCancelled || called(e.calls(), "SendCaptchaEphemeral") || called(e.calls(), "SendCaptchaMessage") || !e.saw(string(CaptchaError)) {
		t.Fatal(e.row(7).State, e.calls(), e.countList())
	}
	e.port.RestrictErr = nil
	if out := e.join(7, "Ada"); out != CaptchaChallenged || e.row(7).Attempt != 2 || e.row(7).State != store.CaptchaChallenged {
		t.Fatal(out, e.row(7))
	}
}

func TestJoinRequestSweepHonoursGateNow(t *testing.T) {
	t.Run("chat captcha off", func(t *testing.T) {
		e := newCap(t)
		e.asJoinRequest("kick")
		if out := e.request(7); out != CaptchaChallenged || e.row(7).MessageID == 0 {
			t.Fatal(out, e.row(7))
		}
		off := false
		cfg := *e.live.Current()
		cfg.Captcha.Chats = map[int64]config.CaptchaChat{capChat: {Enabled: &off}}
		e.live.Swap(&cfg)
		e.due()
		if e.row(7).State != store.CaptchaCancelled || !e.saw(string(CaptchaLeftPending)) || called(e.calls(), "DeclineJoinRequest") {
			t.Fatal(e.row(7).State, e.countList(), e.calls())
		}
	})
	t.Run("force dry-run", func(t *testing.T) {
		e := newCap(t)
		e.asJoinRequest("kick")
		if out := e.request(7); out != CaptchaChallenged || e.row(7).MessageID == 0 {
			t.Fatal(out, e.row(7))
		}
		cfg := *e.live.Current()
		cfg.Chats.ForceDryRun = []int64{capChat}
		e.live.Swap(&cfg)
		e.due()
		if e.row(7).State != store.CaptchaCancelled || !e.saw(string(CaptchaLeftPending)) || called(e.calls(), "DeclineJoinRequest") {
			t.Fatal(e.row(7).State, e.countList(), e.calls())
		}
	})
}

func TestJoinRequestRetries(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		e := newCap(t)
		e.seedDue("join_request", store.CaptchaPassing, "", 0)
		e.port.ApproveErr = errors.New("temp")
		before := e.row(7)
		e.sweep()
		row := e.row(7)
		wantDeadline := e.clock.Add(60 * time.Second).Unix()
		if row.State != store.CaptchaPassing || row.Tries != before.Tries+1 || row.Deadline != wantDeadline {
			t.Fatal(before, row, wantDeadline)
		}
		e.port.ApproveErr = nil
		e.clock = e.clock.Add(60 * time.Second)
		e.sweep()
		if e.row(7).State != store.CaptchaPassed || e.port.LastApprove.Chat != capChat || e.port.LastApprove.User != 7 || !e.saw(string(CaptchaApproved)) {
			t.Fatal(e.row(7), e.port.LastApprove, e.countList())
		}
	})
	t.Run("decline", func(t *testing.T) {
		e := newCap(t)
		e.seedDue("join_request", store.CaptchaFailing, "decline", 0)
		e.port.DeclineErr = errors.New("temp")
		before := e.row(7)
		e.sweep()
		row := e.row(7)
		wantDeadline := e.clock.Add(60 * time.Second).Unix()
		if row.State != store.CaptchaFailing || row.Tries != before.Tries+1 || row.Deadline != wantDeadline {
			t.Fatal(before, row, wantDeadline)
		}
		e.port.DeclineErr = nil
		e.clock = e.clock.Add(60 * time.Second)
		e.sweep()
		if e.row(7).State != store.CaptchaFailed || e.port.LastDecline.Chat != capChat || e.port.LastDecline.User != 7 || !e.saw(string(CaptchaFailedDecline)) {
			t.Fatal(e.row(7), e.port.LastDecline, e.countList())
		}
	})
}

func TestJoinRequestPressIgnoresSanction(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("kick")
	if out := e.request(7); out != CaptchaChallenged {
		t.Fatal(out)
	}
	e.sanction(7, domain.ActionBan)
	spy := &captchaSpy{DB: e.db}
	e.c.Store = spy
	row := e.row(7)
	if out := e.press(7, row.Attempt, 7); out != CaptchaApproved || spy.sanctions != 0 || e.row(7).State != store.CaptchaPassed {
		t.Fatal(out, spy.sanctions, e.row(7).State)
	}
	if e.port.LastApprove.Chat != capChat || e.port.LastApprove.User != 7 {
		t.Fatal(e.port.LastApprove)
	}
}

func TestJoinRequestSweepOrphansAndFailing(t *testing.T) {
	t.Run("orphan new", func(t *testing.T) {
		e := newCap(t)
		if _, started, err := e.db.BeginCaptcha(capChat, 7, e.clock.Unix(), e.clock.Unix()-1, "join_request"); err != nil || !started {
			t.Fatal(started, err)
		}
		e.c = e.engine()
		e.sweep()
		if e.row(7).State != store.CaptchaCancelled || !e.saw(string(CaptchaLeftPending)) || len(e.calls()) != 0 {
			t.Fatal(e.row(7).State, e.countList(), e.calls())
		}
	})
	t.Run("failing decline", func(t *testing.T) {
		e := newCap(t)
		e.seedDue("join_request", store.CaptchaFailing, "decline", 0)
		e.c = e.engine()
		e.sweep()
		if e.row(7).State != store.CaptchaFailed || e.port.LastDecline.Chat != capChat || e.port.LastDecline.User != 7 || !e.saw(string(CaptchaFailedDecline)) {
			t.Fatal(e.row(7), e.port.LastDecline, e.countList())
		}
	})
}
