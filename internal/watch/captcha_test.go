package watch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

const capChat int64 = -100

func durPtr(d time.Duration) *config.Duration {
	v := config.Duration(d)
	return &v
}

func strPtr(s string) *string { return &s }

func ev(restricted bool) telegram.JoinEvent {
	return telegram.JoinEvent{ChatID: capChat, UserID: 7, Restricted: restricted}
}

type capPort struct {
	*fake.Fake
	mu      sync.Mutex
	answers []string
}

func (p *capPort) AnswerCallback(ctx context.Context, id, text string) error {
	p.mu.Lock()
	p.answers = append(p.answers, text)
	p.mu.Unlock()
	return p.Fake.AnswerCallback(ctx, id, text)
}

func (p *capPort) texts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.answers...)
}

type blockRestrict struct {
	*capPort
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockRestrict) RestrictMember(ctx context.Context, chat, user int64, perms telegram.Perms, until int64) error {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return b.Fake.RestrictMember(ctx, chat, user, perms, until)
}

type capEnv struct {
	t      *testing.T
	db     *store.DB
	port   *capPort
	cfg    *config.Config
	live   *config.Store
	c      *Captcha
	clock  time.Time
	mu     sync.Mutex
	counts []string
}

func openWatchDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func newCap(t *testing.T) *capEnv {
	t.Helper()
	off, on := false, true
	e := &capEnv{
		t: t, db: openWatchDB(t), port: &capPort{Fake: fake.New()},
		clock: time.Unix(1_700_000_000, 0),
		cfg: &config.Config{
			Chats: config.ChatsPolicy{Mode: "auto", StartInDryRun: &off},
			Captcha: config.Captcha{
				Enabled: &on, Mode: "button", Timeout: durPtr(30 * time.Second),
				OnFail: "kick", Text: strPtr("prove it"), ButtonText: strPtr("I am not a bot"),
			},
		},
	}
	e.port.CaptchaEphemeralID = 55
	e.live = config.NewStore(e.cfg)
	e.c = e.engine()
	return e
}

func (e *capEnv) engine() *Captcha {
	return &Captcha{
		Config: e.live, Store: e.db, Port: e.port, SelfID: 1,
		Now:   func() time.Time { return e.clock },
		Count: func(r string) { e.mu.Lock(); e.counts = append(e.counts, r); e.mu.Unlock() },
	}
}

func (e *capEnv) join(user int64, name string) CaptchaOutcome {
	e.t.Helper()
	out, err := e.c.OnJoin(context.Background(), telegram.JoinEvent{ChatID: capChat, UserID: user}, name)
	if err != nil {
		e.t.Fatal(err)
	}
	e.c.Wait()
	return out
}

func (e *capEnv) row(user int64) store.CaptchaRow {
	e.t.Helper()
	row, found, err := e.db.GetCaptcha(capChat, user)
	if err != nil || !found {
		e.t.Fatal(user, found, err)
	}
	return row
}

func (e *capEnv) press(user, attempt, presser int64) CaptchaOutcome {
	e.t.Helper()
	out, err := e.c.OnPress(context.Background(), CaptchaPress{
		ID: "cb", Data: fmt.Sprintf("cap:%d:%d:%d", capChat, user, attempt), PresserID: presser,
	})
	if err != nil && out != CaptchaError {
		e.t.Fatal(err)
	}
	return out
}

func (e *capEnv) calls() []string { return e.port.Calls() }

func (e *capEnv) due() {
	e.t.Helper()
	e.clock = e.clock.Add(30 * time.Second)
	if _, err := e.c.Sweep(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func called(calls []string, name string) bool {
	for _, c := range calls {
		if c == name {
			return true
		}
	}
	return false
}

func callAt(calls []string, name string) int {
	for i, c := range calls {
		if c == name {
			return i
		}
	}
	return -1
}

func TestCaptchaSkips(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*capEnv)
		ev    telegram.JoinEvent
		want  CaptchaOutcome
	}{
		{"not admitted", func(e *capEnv) {
			cfg := *e.cfg
			cfg.Chats.Mode = "allowlist"
			e.live.Swap(&cfg)
		}, ev(false), CaptchaSkipNotAdmitted},
		{"disabled", func(e *capEnv) {
			off := false
			cfg := *e.cfg
			cfg.Captcha.Enabled = &off
			e.live.Swap(&cfg)
		}, ev(false), CaptchaSkipDisabled},
		{"chat disabled", func(e *capEnv) {
			if err := e.db.UpsertChat(store.ChatRow{ChatID: capChat, Enabled: false}); err != nil {
				e.t.Fatal(err)
			}
		}, ev(false), CaptchaSkipChatDisabled},
		{"dry-run", func(e *capEnv) {
			cfg := *e.cfg
			cfg.Chats.ForceDryRun = []int64{capChat}
			e.live.Swap(&cfg)
		}, ev(false), CaptchaSkipDryRun},
		{"restricted", nil, ev(true), CaptchaSkipRestricted},
		{"blocklisted", func(e *capEnv) { e.c.Blocklist = listedIDs{7: true} }, ev(false), CaptchaSkipBlocklisted},
		{"passed", func(e *capEnv) {
			row, started, err := e.db.BeginCaptcha(capChat, 7, 1, 1)
			if err != nil || !started {
				e.t.Fatal(err)
			}
			if _, ok, err := e.db.TransitionCaptcha(capChat, 7, row.Attempt, []string{store.CaptchaNew}, store.CaptchaPassed); err != nil || !ok {
				e.t.Fatal(err)
			}
		}, ev(false), CaptchaSkipKnown},
		{"trusted", func(e *capEnv) {
			if _, err := e.db.BumpTrust(capChat, 7); err != nil {
				e.t.Fatal(err)
			}
		}, ev(false), CaptchaSkipKnown},
		{"pending", func(e *capEnv) {
			if _, started, err := e.db.BeginCaptcha(capChat, 7, 1, e.clock.Add(time.Hour).Unix()); err != nil || !started {
				e.t.Fatal(err)
			}
		}, ev(false), CaptchaSkipPending},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			e := newCap(t)
			if tt.setup != nil {
				tt.setup(e)
			}
			out, err := e.c.OnJoin(context.Background(), tt.ev, "Ada")
			e.c.Wait()
			if err != nil || out != tt.want || called(e.calls(), "RestrictMember") {
				t.Fatal(out, err, e.calls(), tt.want)
			}
		})
	}
	t.Run("welcomed still challenged", func(t *testing.T) {
		e := newCap(t)
		if err := e.db.MarkWelcomed(capChat, 7); err != nil {
			t.Fatal(err)
		}
		if out := e.join(7, "Ada"); out != CaptchaChallenged || e.row(7).State != store.CaptchaChallenged || !called(e.calls(), "RestrictMember") {
			t.Fatal(out, e.row(7).State, e.calls())
		}
	})
}

func TestCaptchaMutesThenPrompts(t *testing.T) {
	e := newCap(t)
	if out := e.join(7, "Ada"); out != CaptchaChallenged {
		t.Fatal(out)
	}
	calls := e.calls()
	if callAt(calls, "RestrictMember") < 0 || callAt(calls, "SendCaptchaEphemeral") < callAt(calls, "RestrictMember") {
		t.Fatal(calls)
	}
	row := e.row(7)
	if row.State != store.CaptchaChallenged || row.Attempt != 1 {
		t.Fatal(row)
	}
	if e.port.LastRestrict.Perms.CanSend || e.port.LastRestrict.Until != 0 {
		t.Fatal(e.port.LastRestrict)
	}
	if got := e.port.LastCaptchaEphemeral.Buttons[0][0].Data; got != fmt.Sprintf("cap:%d:7:%d", capChat, row.Attempt) {
		t.Fatal(got)
	}
}

func TestCaptchaFallsBackToChatMessage(t *testing.T) {
	e := newCap(t)
	e.port.CaptchaEphemeralErr = errors.New("not honored")
	e.port.CaptchaMessageID = 77
	e.join(7, "Ada")
	row := e.row(7)
	if row.State != store.CaptchaChallenged || row.MessageID != 77 || row.EphemeralID != 0 {
		t.Fatal(row)
	}
	if e.port.LastCaptchaMessage.Text != "Ada, prove it" {
		t.Fatal(e.port.LastCaptchaMessage.Text)
	}
	e.join(8, "  ")
	if e.port.LastCaptchaMessage.Text != "prove it" {
		t.Fatal(e.port.LastCaptchaMessage.Text)
	}
}

func TestCaptchaPromptFailureFailsOpen(t *testing.T) {
	e := newCap(t)
	e.port.CaptchaEphemeralErr = errors.New("nope")
	e.port.CaptchaMessageErr = errors.New("nope")
	e.join(7, "Ada")
	if e.row(7).State != store.CaptchaCancelled || !called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
		t.Fatal(e.row(7).State, e.calls())
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.counts) != 1 || e.counts[0] != string(CaptchaReleased) {
		t.Fatal(e.counts)
	}
}

func TestCaptchaPressPasses(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	row := e.row(7)
	if out := e.press(7, row.Attempt, 7); out != CaptchaPassed || e.row(7).State != store.CaptchaPassed {
		t.Fatal(out, e.row(7).State)
	}
	if e.port.LastUnrestrict.UserID != 7 || e.port.LastDeleteEphemeral.EphemeralID != 55 {
		t.Fatal(e.port.LastUnrestrict, e.port.LastDeleteEphemeral)
	}
	if texts := e.port.texts(); len(texts) != 1 || texts[0] != "confirmed" {
		t.Fatal(texts)
	}
}

func TestCaptchaPressByOtherUserRejected(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	row := e.row(7)
	if out := e.press(7, row.Attempt, 99); out != CaptchaWrongUser || e.row(7).State != store.CaptchaChallenged || called(e.calls(), "UnrestrictMember") {
		t.Fatal(out, e.row(7).State, e.calls())
	}
	if texts := e.port.texts(); len(texts) != 1 || texts[0] != "not for you" {
		t.Fatal(texts)
	}
}

func TestCaptchaStaleAttemptIgnored(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	if out, err := e.c.OnMemberChange(context.Background(), telegram.MemberChange{ChatID: capChat, UserID: 7, ActorID: 50}); err != nil || out != CaptchaCancelledByAdmin {
		t.Fatal(out, err)
	}
	e.join(7, "Ada")
	row := e.row(7)
	if row.Attempt != 2 || row.State != store.CaptchaChallenged {
		t.Fatal(row)
	}
	if out := e.press(7, 1, 7); out != CaptchaExpired || e.row(7).State != store.CaptchaChallenged || called(e.calls(), "UnrestrictMember") {
		t.Fatal(out, e.row(7).State, e.calls())
	}
}

func TestCaptchaPressAfterSanctionKeepsMute(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	if _, _, err := e.db.InsertPending(capChat, 1, 7, 0, false, domain.Verdict{Action: domain.ActionMute}); err != nil {
		t.Fatal(err)
	}
	row := e.row(7)
	if out := e.press(7, row.Attempt, 7); out != CaptchaCancelledSanction || e.row(7).State != store.CaptchaCancelled || called(e.calls(), "UnrestrictMember") {
		t.Fatal(out, e.row(7).State, e.calls())
	}
	if texts := e.port.texts(); len(texts) != 1 || texts[0] != "moderators will review" {
		t.Fatal(texts)
	}
}

func TestCaptchaAdminChangeCancels(t *testing.T) {
	e := newCap(t)
	e.join(7, "A")
	e.join(8, "B")
	e.join(9, "C")
	out, err := e.c.OnMemberChange(context.Background(), telegram.MemberChange{ChatID: capChat, UserID: 7, ActorID: 50})
	if err != nil || out != CaptchaCancelledByAdmin || e.row(7).State != store.CaptchaCancelled {
		t.Fatal(out, e.row(7).State, err)
	}
	out, err = e.c.OnMemberChange(context.Background(), telegram.MemberChange{ChatID: capChat, UserID: 8, ActorID: e.c.SelfID})
	if err != nil || out != CaptchaSkip || e.row(8).State != store.CaptchaChallenged {
		t.Fatal(out, e.row(8).State, err)
	}
	out, err = e.c.OnMemberChange(context.Background(), telegram.MemberChange{ChatID: capChat, UserID: 9, ActorID: 9})
	if err != nil || out != CaptchaSkip || e.row(9).State != store.CaptchaChallenged {
		t.Fatal(out, e.row(9).State, err)
	}
	if called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
		t.Fatal(e.calls())
	}
	if e.port.LastDeleteEphemeral.UserID != 7 {
		t.Fatal(e.port.LastDeleteEphemeral)
	}
}

func TestCaptchaTimeoutKicks(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	e.due()
	calls := e.calls()
	if e.row(7).State != store.CaptchaFailed || callAt(calls, "BanMember") < 0 || callAt(calls, "UnbanMember") < callAt(calls, "BanMember") {
		t.Fatal(e.row(7).State, calls)
	}
}

func TestCaptchaKickRetriedAfterError(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	e.port.UnbanErr = errors.New("temp")
	e.due()
	row := e.row(7)
	if row.State != store.CaptchaFailing || row.Tries < 1 {
		t.Fatal(row)
	}
	e.port.UnbanErr = nil
	e.clock = e.clock.Add(60 * time.Second)
	if _, err := e.c.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.row(7).State != store.CaptchaFailed {
		t.Fatal(e.row(7))
	}
}

func TestCaptchaTimeoutKeepMuted(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	cfg := *e.cfg
	cfg.Captcha.OnFail = "keep_muted"
	e.live.Swap(&cfg)
	e.due()
	if e.row(7).State != store.CaptchaFailed || called(e.calls(), "BanMember") || called(e.calls(), "UnrestrictMember") {
		t.Fatal(e.row(7).State, e.calls())
	}
}

func TestCaptchaSweepFailsOpenWhenDryRunNow(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	cfg := *e.cfg
	cfg.Chats.ForceDryRun = []int64{capChat}
	e.live.Swap(&cfg)
	e.due()
	if e.row(7).State != store.CaptchaCancelled || !called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
		t.Fatal(e.row(7).State, e.calls())
	}
}

func TestCaptchaSweepSkipsInFlightRestrict(t *testing.T) {
	e := newCap(t)
	block := &blockRestrict{capPort: e.port, started: make(chan struct{}), release: make(chan struct{})}
	e.c.Port = block
	out, err := e.c.OnJoin(context.Background(), ev(false), "Ada")
	if err != nil || out != CaptchaChallenged {
		t.Fatal(out, err)
	}
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("restrict did not start")
	}
	e.clock = e.clock.Add(31 * time.Second)
	if _, err := e.c.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, found, err := e.db.GetCaptcha(capChat, 7)
	if err != nil || !found || row.State != store.CaptchaNew || called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
		t.Fatal(row, found, err, e.calls())
	}
	close(block.release)
	e.c.Wait()
	if e.row(7).State != store.CaptchaChallenged {
		t.Fatal(e.row(7))
	}
}

func TestCaptchaPressAfterTimeoutIgnored(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	cfg := *e.cfg
	cfg.Captcha.OnFail = "keep_muted"
	e.live.Swap(&cfg)
	e.due()
	row := e.row(7)
	if out := e.press(7, row.Attempt, 7); out != CaptchaExpired || e.row(7).State != store.CaptchaFailed || called(e.calls(), "UnrestrictMember") {
		t.Fatal(out, e.row(7).State, e.calls())
	}
}

func TestCaptchaSurvivesRestart(t *testing.T) {
	e := newCap(t)
	row, started, err := e.db.BeginCaptcha(capChat, 1, 100, 100)
	if err != nil || !started {
		t.Fatal(err)
	}
	if _, ok, err := e.db.TransitionCaptcha(capChat, 1, row.Attempt, []string{store.CaptchaNew}, store.CaptchaChallenged); err != nil || !ok {
		t.Fatal(err)
	}
	if _, started, err = e.db.BeginCaptcha(capChat, 2, 100, 100); err != nil || !started {
		t.Fatal(err)
	}
	e.c = e.engine()
	if _, err := e.c.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.row(1).State != store.CaptchaFailed || e.row(2).State != store.CaptchaCancelled {
		t.Fatal(e.row(1).State, e.row(2).State, e.calls())
	}
	if !called(e.calls(), "BanMember") || !called(e.calls(), "UnbanMember") || !called(e.calls(), "UnrestrictMember") {
		t.Fatal(e.calls())
	}
}

type unrestrictGate struct {
	telegram.Port
	err error
}

func (g *unrestrictGate) UnrestrictMember(ctx context.Context, chat, user int64) error {
	if g.err != nil {
		return g.err
	}
	return g.Port.UnrestrictMember(ctx, chat, user)
}

type errPrompt struct{ *store.DB }

func (errPrompt) SetCaptchaPrompt(int64, int64, int64, int, int, int64) (store.CaptchaRow, error) {
	return store.CaptchaRow{}, errors.New("save prompt")
}

type errPromptAndFail struct{ *store.DB }

func (errPromptAndFail) SetCaptchaPrompt(int64, int64, int64, int, int, int64) (store.CaptchaRow, error) {
	return store.CaptchaRow{}, errors.New("save prompt")
}

func (errPromptAndFail) FailCaptcha(int64, int64, int64, []string, string) (store.CaptchaRow, bool, error) {
	return store.CaptchaRow{}, false, errors.New("db")
}

func TestCaptchaChallengedCounted(t *testing.T) {
	e := newCap(t)
	out := e.join(7, "Ada")
	if out != CaptchaChallenged || string(out) != "challenged" {
		t.Fatal(out)
	}
	if e.row(7).State != store.CaptchaChallenged || out == "" || out == CaptchaSkip {
		t.Fatal(out, e.row(7).State)
	}
}

func TestCaptchaPassingSurvivesCrash(t *testing.T) {
	e := newCap(t)
	row, started, err := e.db.BeginCaptcha(capChat, 7, e.clock.Unix(), e.clock.Unix())
	if err != nil || !started {
		t.Fatal(err)
	}
	if _, ok, err := e.db.TransitionCaptcha(capChat, 7, row.Attempt, []string{store.CaptchaNew}, store.CaptchaPassing); err != nil || !ok {
		t.Fatal(err)
	}
	known, err := e.db.CaptchaPassed(capChat, 7)
	if err != nil || known {
		t.Fatal(known, err)
	}
	e.c = e.engine()
	if _, err := e.c.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.row(7).State != store.CaptchaPassed || !called(e.calls(), "UnrestrictMember") {
		t.Fatal(e.row(7).State, e.calls())
	}
}

func TestCaptchaPressUnrestrictErrorKeepsPassing(t *testing.T) {
	e := newCap(t)
	gate := &unrestrictGate{Port: e.port, err: errors.New("temp")}
	e.c.Port = gate
	e.join(7, "Ada")
	row := e.row(7)
	out, err := e.c.OnPress(context.Background(), CaptchaPress{
		ID: "cb", Data: fmt.Sprintf("cap:%d:%d:%d", capChat, 7, row.Attempt), PresserID: 7,
	})
	if out != CaptchaError || err == nil || e.row(7).State != store.CaptchaPassing || e.row(7).Tries != 0 {
		t.Fatal(out, err, e.row(7))
	}
	if texts := e.port.texts(); len(texts) != 1 || texts[0] != "accepted, access will be restored shortly" {
		t.Fatal(texts)
	}
	if _, err := e.c.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	row = e.row(7)
	if row.State != store.CaptchaPassing || row.Tries != 1 || called(e.calls(), "UnrestrictMember") {
		t.Fatal(row, e.calls())
	}
	gate.err = nil
	e.clock = e.clock.Add(60 * time.Second)
	if _, err := e.c.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.row(7).State != store.CaptchaPassed || !called(e.calls(), "UnrestrictMember") {
		t.Fatal(e.row(7).State, e.calls())
	}
}

func TestCaptchaFailOpenRespectsSanction(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	if _, _, err := e.db.InsertPending(capChat, 1, 7, 0, false, domain.Verdict{Action: domain.ActionBan}); err != nil {
		t.Fatal(err)
	}
	cfg := *e.cfg
	cfg.Chats.ForceDryRun = []int64{capChat}
	e.live.Swap(&cfg)
	e.due()
	if e.row(7).State != store.CaptchaCancelled || called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
		t.Fatal(e.row(7).State, e.calls())
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.counts) != 1 || e.counts[0] != string(CaptchaCancelledSanction) {
		t.Fatal(e.counts)
	}
}

func TestCaptchaPromptSaveErrorFailsOpen(t *testing.T) {
	e := newCap(t)
	e.c.Store = errPrompt{e.db}
	e.join(7, "Ada")
	if e.row(7).State != store.CaptchaCancelled || e.port.LastDeleteEphemeral.EphemeralID != 55 {
		t.Fatal(e.row(7).State, e.port.LastDeleteEphemeral)
	}
	if !called(e.calls(), "UnrestrictMember") || called(e.calls(), "BanMember") {
		t.Fatal(e.calls())
	}
	e2 := newCap(t)
	e2.c.Store = errPromptAndFail{e2.db}
	e2.join(7, "Ada")
	if e2.row(7).State != store.CaptchaChallenged || called(e2.calls(), "DeleteEphemeral") || called(e2.calls(), "UnrestrictMember") {
		t.Fatal(e2.row(7).State, e2.calls())
	}
}

func TestCaptchaDeadlineStartsAtPrompt(t *testing.T) {
	e := newCap(t)
	block := &blockRestrict{capPort: e.port, started: make(chan struct{}), release: make(chan struct{})}
	e.c.Port = block
	inserted := e.clock
	out, err := e.c.OnJoin(context.Background(), ev(false), "Ada")
	if err != nil || out != CaptchaChallenged {
		t.Fatal(out, err)
	}
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("restrict did not start")
	}
	e.clock = e.clock.Add(10 * time.Second)
	close(block.release)
	e.c.Wait()
	row := e.row(7)
	want := e.clock.Add(30 * time.Second).Unix()
	if row.Deadline != want || row.Deadline == inserted.Add(30*time.Second).Unix() {
		t.Fatalf("deadline %d, want %d from the send, not %d from the join", row.Deadline, want, inserted.Add(30*time.Second).Unix())
	}
}

func TestCaptchaRestrictAfterCancelCounted(t *testing.T) {
	e := newCap(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	block := &blockRestrict{capPort: e.port, started: make(chan struct{}), release: make(chan struct{})}
	e.c.Port = block
	out, err := e.c.OnJoin(context.Background(), ev(false), "Ada")
	if err != nil || out != CaptchaChallenged {
		t.Fatal(out, err)
	}
	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("restrict did not start")
	}
	cout, err := e.c.OnMemberChange(context.Background(), telegram.MemberChange{ChatID: capChat, UserID: 7, ActorID: 50})
	if err != nil || cout != CaptchaCancelledByAdmin || e.row(7).State != store.CaptchaCancelled {
		t.Fatal(cout, err, e.row(7).State)
	}
	close(block.release)
	e.c.Wait()
	if e.row(7).State != store.CaptchaCancelled || !called(e.calls(), "RestrictMember") || called(e.calls(), "UnrestrictMember") {
		t.Fatal(e.row(7).State, e.calls())
	}
	if !strings.Contains(buf.String(), "restrict after cancel chat=-100 user=7") {
		t.Fatal(buf.String())
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	found := false
	for _, c := range e.counts {
		if c == string(CaptchaRestrictAfterCancel) {
			found = true
		}
	}
	if !found {
		t.Fatal(e.counts)
	}
}
