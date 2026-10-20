package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

func orphanButtonChallenge(t *testing.T, e *capEnv) {
	t.Helper()
	row, started, err := e.db.BeginCaptcha(capChat, 7, e.clock.Unix(), e.clock.Unix(), "button")
	if err != nil || !started {
		t.Fatalf("begin: started=%v err=%v", started, err)
	}
	if _, moved, err := e.db.TransitionCaptcha(capChat, 7, row.Attempt, []string{store.CaptchaNew}, store.CaptchaChallenged); err != nil || !moved {
		t.Fatalf("challenge: moved=%v err=%v", moved, err)
	}
}

func appliedCaptchaSanction(t *testing.T, e *capEnv) {
	t.Helper()
	id, _, err := e.db.InsertPending(capChat, 123, 7, 0, false, domain.Verdict{Action: domain.ActionMute})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.SetIncidentState(id, domain.StateActed); err != nil {
		t.Fatal(err)
	}
}

func TestCaptchaOrphanButtonReleased(t *testing.T) {
	for _, policy := range []string{"kick", "keep_muted"} {
		t.Run(policy, func(t *testing.T) {
			e := newCap(t)
			cfg := *e.cfg
			cfg.Captcha.OnFail = policy
			e.live.Swap(&cfg)
			orphanButtonChallenge(t, e)
			e.due()
			if e.row(7).State != store.CaptchaCancelled || !called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
				t.Fatalf("row=%+v calls=%v", e.row(7), e.calls())
			}
		})
	}
}

func TestCaptchaOrphanButtonPreservesSanction(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			e := newCap(t)
			orphanButtonChallenge(t, e)
			appliedCaptchaSanction(t, e)
			if disabled {
				cfg := *e.cfg
				off := false
				cfg.Captcha.Enabled = &off
				e.live.Swap(&cfg)
			}
			e.due()
			if e.row(7).State != store.CaptchaCancelled || called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
				t.Fatalf("row=%+v calls=%v", e.row(7), e.calls())
			}
		})
	}
}

func TestCaptchaOrphanReleaseRetryChecksSanction(t *testing.T) {
	for _, sanctioned := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "sanction"}[sanctioned], func(t *testing.T) {
			e := newCap(t)
			orphanButtonChallenge(t, e)
			gate := &unrestrictGate{Port: e.port, err: errors.New("temporary unrestrict failure")}
			e.c.Port = gate
			e.due()
			if row := e.row(7); row.State != store.CaptchaFailing || row.FailAction != "unrestrict" || row.Tries != 1 {
				t.Fatalf("retry row=%+v", row)
			}
			if sanctioned {
				appliedCaptchaSanction(t, e)
			}
			gate.err = nil
			e.clock = e.clock.Add(time.Minute)
			if _, err := e.c.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			if e.row(7).State != store.CaptchaCancelled || called(e.calls(), "UnrestrictMember") == sanctioned || called(e.calls(), "BanMember") {
				t.Fatalf("row=%+v calls=%v", e.row(7), e.calls())
			}
		})
	}
}

type blockedCaptchaPrompt struct {
	telegram.Port
	started chan struct{}
	release chan struct{}
}

func (p *blockedCaptchaPrompt) SendCaptchaEphemeral(ctx context.Context, chat, user int64, text string, buttons [][]telegram.Button) (int, error) {
	close(p.started)
	<-p.release
	return p.Port.SendCaptchaEphemeral(ctx, chat, user, text, buttons)
}

func TestCaptchaSweepSkipsInFlightButtonPrompt(t *testing.T) {
	e := newCap(t)
	blocked := &blockedCaptchaPrompt{Port: e.port, started: make(chan struct{}), release: make(chan struct{})}
	e.c.Port = blocked
	defer func() { close(blocked.release); e.c.Wait() }()
	if _, err := e.c.OnJoin(context.Background(), ev(false), "Ada"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not start")
	}
	e.due()
	if e.row(7).State != store.CaptchaChallenged || called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
		t.Fatalf("row=%+v calls=%v", e.row(7), e.calls())
	}
}
