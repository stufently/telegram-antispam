package watch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

const userChat int64 = 700

func (e *capEnv) asJoinRequest(onFail string) {
	e.t.Helper()
	cfg := *e.cfg
	cfg.Captcha.Mode = "join_request"
	if onFail != "" {
		cfg.Captcha.OnFail = onFail
	}
	e.live.Swap(&cfg)
	e.port.CaptchaMessageID = 77
}

func (e *capEnv) request(user int64) CaptchaOutcome {
	e.t.Helper()
	out, err := e.c.OnJoinRequest(context.Background(), telegram.JoinRequest{
		ChatID: capChat, UserID: user, UserChatID: userChat, DisplayName: "Ada",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.c.Wait()
	return out
}

func (e *capEnv) saw(result string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.counts {
		if c == result {
			return true
		}
	}
	return false
}

func settled(calls []string) bool {
	for _, c := range calls {
		switch c {
		case "ApproveJoinRequest", "DeclineJoinRequest", "SendCaptchaMessage", "RestrictMember", "DeleteMessages", "BanMember", "UnbanMember", "UnrestrictMember":
			return true
		}
	}
	return false
}

func TestJoinRequestSkips(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*capEnv)
		want  CaptchaOutcome
	}{
		{"not admitted", func(e *capEnv) {
			e.asJoinRequest("")
			cfg := *e.cfg
			cfg.Chats.Mode = "allowlist"
			cfg.Captcha.Mode = "join_request"
			e.live.Swap(&cfg)
		}, CaptchaSkipNotAdmitted},
		{"disabled", func(e *capEnv) {
			off := false
			cfg := *e.cfg
			cfg.Captcha.Enabled = &off
			cfg.Captcha.Mode = "join_request"
			e.live.Swap(&cfg)
		}, CaptchaSkipDisabled},
		{"chat disabled", func(e *capEnv) {
			e.asJoinRequest("")
			if err := e.db.UpsertChat(store.ChatRow{ChatID: capChat, Enabled: false}); err != nil {
				e.t.Fatal(err)
			}
		}, CaptchaSkipChatDisabled},
		{"dry-run", func(e *capEnv) {
			cfg := *e.cfg
			cfg.Captcha.Mode = "join_request"
			cfg.Chats.ForceDryRun = []int64{capChat}
			e.live.Swap(&cfg)
		}, CaptchaSkipDryRun},
		{"other mode", nil, CaptchaSkipMode},
		{"blocklisted", func(e *capEnv) {
			e.asJoinRequest("")
			e.c.Blocklist = listedIDs{7: true}
		}, CaptchaSkipBlocklisted},
		{"pending", func(e *capEnv) {
			e.asJoinRequest("")
			if _, started, err := e.db.BeginCaptcha(capChat, 7, 1, e.clock.Add(time.Hour).Unix(), "join_request"); err != nil || !started {
				e.t.Fatal(err)
			}
		}, CaptchaSkipPending},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			e := newCap(t)
			if tt.setup != nil {
				tt.setup(e)
			}
			out, err := e.c.OnJoinRequest(context.Background(), telegram.JoinRequest{
				ChatID: capChat, UserID: 7, UserChatID: userChat, DisplayName: "Ada",
			})
			e.c.Wait()
			if err != nil || out != tt.want || settled(e.calls()) {
				t.Fatal(out, err, e.calls(), tt.want)
			}
		})
	}
}

func TestJoinRequestPromptsInPrivate(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	if out := e.request(7); out != CaptchaChallenged {
		t.Fatal(out)
	}
	msg := e.port.LastCaptchaMessage
	if msg.Chat != userChat || msg.Text != "prove it" || len(msg.Buttons) != 1 || msg.Buttons[0][0].Data != "cap:-100:7:1" {
		t.Fatal(msg)
	}
	row := e.row(7)
	if row.State != store.CaptchaChallenged || row.Mode != "join_request" || row.PromptChatID != userChat || row.MessageID != 77 {
		t.Fatal(row)
	}
}

func TestJoinRequestPressApproves(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	e.request(7)
	row := e.row(7)
	if out := e.press(7, row.Attempt, 7); out != CaptchaApproved || e.row(7).State != store.CaptchaPassed {
		t.Fatal(out, e.row(7).State)
	}
	if e.port.LastApprove.Chat != capChat || e.port.LastApprove.User != 7 {
		t.Fatal(e.port.LastApprove)
	}
	if e.port.LastDelete.Chat != userChat || len(e.port.LastDelete.IDs) != 1 || e.port.LastDelete.IDs[0] != 77 {
		t.Fatal(e.port.LastDelete)
	}
	if called(e.calls(), "UnrestrictMember") {
		t.Fatal(e.calls())
	}
}

func TestJoinRequestWrongUserRejected(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	e.request(7)
	row := e.row(7)
	if out := e.press(7, row.Attempt, 9); out != CaptchaWrongUser || e.row(7).State != store.CaptchaChallenged || called(e.calls(), "ApproveJoinRequest") {
		t.Fatal(out, e.row(7).State, e.calls())
	}
}

func TestJoinRequestTimeoutDeclines(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	e.request(7)
	e.due()
	row := e.row(7)
	if row.State != store.CaptchaFailed || row.FailAction != "decline" {
		t.Fatal(row.State, row.FailAction)
	}
	if e.port.LastDecline.Chat != capChat || e.port.LastDecline.User != 7 || called(e.calls(), "ApproveJoinRequest") {
		t.Fatal(e.port.LastDecline, e.calls())
	}
	if e.port.LastDelete.Chat != userChat || !e.saw("failed_decline") {
		t.Fatal(e.port.LastDelete, e.saw("failed_decline"))
	}
}

func TestJoinRequestKeepMutedLeavesPending(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("keep_muted")
	e.request(7)
	e.due()
	if e.row(7).State != store.CaptchaCancelled {
		t.Fatal(e.row(7).State)
	}
	if called(e.calls(), "ApproveJoinRequest") || called(e.calls(), "DeclineJoinRequest") || !e.saw("left_pending") {
		t.Fatal(e.calls(), e.saw("left_pending"))
	}
	if e.port.LastDelete.Chat != userChat {
		t.Fatal(e.port.LastDelete)
	}
}

func TestJoinRequestPromptFailureLeavesForAdmins(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	e.port.CaptchaMessageErr = errors.New("dm closed")
	out, err := e.c.OnJoinRequest(context.Background(), telegram.JoinRequest{
		ChatID: capChat, UserID: 7, UserChatID: userChat, DisplayName: "Ada",
	})
	e.c.Wait()
	if err != nil || out != CaptchaChallenged || e.row(7).State != store.CaptchaCancelled {
		t.Fatal(out, err, e.row(7).State)
	}
	if called(e.calls(), "ApproveJoinRequest") || called(e.calls(), "DeclineJoinRequest") || called(e.calls(), "DeleteMessages") || !e.saw("prompt_failed") {
		t.Fatal(e.calls(), e.saw("prompt_failed"))
	}
}

func TestJoinRequestKnownApproved(t *testing.T) {
	t.Run("passed", func(t *testing.T) {
		e := newCap(t)
		e.asJoinRequest("")
		if _, started, err := e.db.BeginCaptcha(capChat, 7, 1, 1, "button"); err != nil || !started {
			t.Fatal(err)
		}
		if _, ok, err := e.db.TransitionCaptcha(capChat, 7, 1, []string{store.CaptchaNew}, store.CaptchaPassed); err != nil || !ok {
			t.Fatal(err)
		}
		if out := e.request(7); out != CaptchaApprovedKnown || e.row(7).State != store.CaptchaPassed || e.row(7).Attempt != 1 {
			t.Fatal(out, e.row(7))
		}
		if e.port.LastApprove.Chat != capChat || e.port.LastApprove.User != 7 || called(e.calls(), "SendCaptchaMessage") {
			t.Fatal(e.port.LastApprove, e.calls())
		}
	})
	t.Run("trusted", func(t *testing.T) {
		e := newCap(t)
		e.asJoinRequest("")
		if _, err := e.db.BumpTrust(capChat, 7); err != nil {
			t.Fatal(err)
		}
		if out := e.request(7); out != CaptchaApprovedKnown {
			t.Fatal(out)
		}
		if _, found, err := e.db.GetCaptcha(capChat, 7); err != nil || found {
			t.Fatal(found, err)
		}
		if e.port.LastApprove.User != 7 || called(e.calls(), "SendCaptchaMessage") {
			t.Fatal(e.port.LastApprove, e.calls())
		}
	})
}

func TestJoinRequestApproveErrorRetried(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	e.port.ApproveErr = errors.New("temp")
	e.request(7)
	row := e.row(7)
	out, err := e.c.OnPress(context.Background(), CaptchaPress{
		ID: "cb", Data: fmt.Sprintf("cap:%d:%d:%d", capChat, 7, row.Attempt), PresserID: 7,
	})
	if out != CaptchaError || err == nil || e.row(7).State != store.CaptchaPassing {
		t.Fatal(out, err, e.row(7).State)
	}
	e.port.ApproveErr = nil
	if _, err := e.c.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.row(7).State != store.CaptchaPassed {
		t.Fatal(e.row(7).State)
	}
	n := 0
	for _, c := range e.calls() {
		if c == "ApproveJoinRequest" {
			n++
		}
	}
	if n != 2 || e.port.LastApprove.User != 7 {
		t.Fatal(n, e.calls())
	}
}

func TestJoinRequestAdminApproveCancels(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	e.request(7)
	out, err := e.c.OnMemberChange(context.Background(), telegram.MemberChange{ChatID: capChat, UserID: 7, ActorID: 50})
	if err != nil || out != CaptchaCancelledByAdmin || e.row(7).State != store.CaptchaCancelled {
		t.Fatal(out, err, e.row(7).State)
	}
	if e.port.LastDelete.Chat != userChat || len(e.port.LastDelete.IDs) != 1 || e.port.LastDelete.IDs[0] != 77 {
		t.Fatal(e.port.LastDelete)
	}
	if called(e.calls(), "ApproveJoinRequest") || called(e.calls(), "DeclineJoinRequest") {
		t.Fatal(e.calls())
	}
}

func TestJoinInJoinRequestChatSkipsButton(t *testing.T) {
	e := newCap(t)
	e.asJoinRequest("")
	out, err := e.c.OnJoin(context.Background(), telegram.JoinEvent{ChatID: capChat, UserID: 7}, "Ada")
	e.c.Wait()
	if err != nil || out != CaptchaSkipMode || settled(e.calls()) {
		t.Fatal(out, err, e.calls())
	}
	if _, found, err := e.db.GetCaptcha(capChat, 7); err != nil || found {
		t.Fatal(found, err)
	}
}
