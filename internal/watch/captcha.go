package watch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

type CaptchaOutcome string

const (
	CaptchaSkip              CaptchaOutcome = "skip"
	CaptchaSkipNotAdmitted   CaptchaOutcome = "skip_not_admitted"
	CaptchaSkipDisabled      CaptchaOutcome = "skip_disabled"
	CaptchaSkipChatDisabled  CaptchaOutcome = "skip_chat_disabled"
	CaptchaSkipDryRun        CaptchaOutcome = "skip_dry_run"
	CaptchaSkipRestricted    CaptchaOutcome = "skip_restricted"
	CaptchaSkipBlocklisted   CaptchaOutcome = "skip_blocklisted"
	CaptchaSkipKnown         CaptchaOutcome = "skip_known"
	CaptchaSkipPending       CaptchaOutcome = "skip_pending"
	CaptchaPassed            CaptchaOutcome = "passed"
	CaptchaReleased          CaptchaOutcome = "released"
	CaptchaFailedKick        CaptchaOutcome = "failed_kick"
	CaptchaFailedMuted       CaptchaOutcome = "failed_muted"
	CaptchaGaveUp            CaptchaOutcome = "gave_up"
	CaptchaCancelledByAdmin  CaptchaOutcome = "cancelled_by_admin"
	CaptchaInvalid           CaptchaOutcome = "invalid"
	CaptchaWrongUser         CaptchaOutcome = "wrong_user"
	CaptchaExpired           CaptchaOutcome = "expired"
	CaptchaCancelledSanction CaptchaOutcome = "cancelled_sanctioned"
	CaptchaError             CaptchaOutcome = "error"
	captchaGiveUpTries                      = 5
	captchaRetryDelay                       = 60 * time.Second
)

type CaptchaStore interface {
	BeginCaptcha(chatID, userID, now, deadline int64) (store.CaptchaRow, bool, error)
	CaptchaPassed(chatID, userID int64) (bool, error)
	GetCaptcha(chatID, userID int64) (store.CaptchaRow, bool, error)
	TransitionCaptcha(chatID, userID, attempt int64, from []string, to string) (store.CaptchaRow, bool, error)
	FailCaptcha(chatID, userID, attempt int64, from []string, action string) (store.CaptchaRow, bool, error)
	RetryCaptcha(chatID, userID, attempt, nextDeadline int64) error
	SetCaptchaPrompt(chatID, userID, attempt int64, ephemeralID, messageID int) (store.CaptchaRow, error)
	DueCaptchas(now int64, limit int) ([]store.CaptchaRow, error)
	SanctionSince(chatID, userID, since int64) (bool, error)
	TrustCount(chatID, userID int64) (int, error)
	GetChat(chatID int64) (store.ChatRow, bool, error)
}

var _ CaptchaStore = (*store.DB)(nil)

type CaptchaPress struct {
	ID        string
	Data      string
	PresserID int64
}

type captchaPair struct{ chat, user int64 }

type Captcha struct {
	Config    *config.Store
	Store     CaptchaStore
	Port      telegram.Port
	Blocklist detect.BlocklistSource
	SelfID    int64
	Now       func() time.Time
	Count     func(result string)

	mu       sync.Mutex
	inflight map[captchaPair]struct{}
	wg       sync.WaitGroup
}

func (c *Captcha) Go(fn func()) {
	if fn == nil {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("captcha: panic: %v", rec)
			}
		}()
		fn()
	}()
}

func (c *Captcha) Wait() { c.wg.Wait() }

func IsCaptchaCallback(data string) bool {
	return strings.HasPrefix(data, "cap:")
}

func captchaData(chatID, userID, attempt int64) string {
	return fmt.Sprintf("cap:%d:%d:%d", chatID, userID, attempt)
}

func parseCaptchaData(data string) (chatID, userID, attempt int64, ok bool) {
	rest, found := strings.CutPrefix(data, "cap:")
	if !found {
		return 0, 0, 0, false
	}
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if chatID, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if userID, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if attempt, err = strconv.ParseInt(parts[2], 10, 64); err != nil || attempt < 1 {
		return 0, 0, 0, false
	}
	return chatID, userID, attempt, true
}

func (c *Captcha) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Captcha) count(out CaptchaOutcome) {
	if c != nil && c.Count != nil && out != "" && out != CaptchaSkip {
		c.Count(string(out))
	}
}

func (c *Captcha) log(err error) {
	log.Printf("captcha: %v", err)
	c.count(CaptchaError)
}

func (c *Captcha) track(chatID, userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight == nil {
		c.inflight = map[captchaPair]struct{}{}
	}
	c.inflight[captchaPair{chatID, userID}] = struct{}{}
}

func (c *Captcha) tracking(chatID, userID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.inflight[captchaPair{chatID, userID}]
	return ok
}

func (c *Captcha) untrack(chatID, userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, captchaPair{chatID, userID})
}

func (c *Captcha) gate(chatID int64) (CaptchaOutcome, config.CaptchaPolicy, error) {
	var pol config.CaptchaPolicy
	if c.Config == nil {
		return CaptchaError, pol, errors.New("captcha: no config")
	}
	cfg := c.Config.Current()
	if cfg == nil {
		return CaptchaError, pol, errors.New("captcha: no config")
	}
	if !telegram.RegisteredChat(cfg, chatID) {
		return CaptchaSkipNotAdmitted, pol, nil
	}
	var on bool
	pol, on = cfg.Captcha.For(chatID)
	if !on {
		return CaptchaSkipDisabled, pol, nil
	}
	row, found, err := c.Store.GetChat(chatID)
	if err != nil {
		return CaptchaError, pol, err
	}
	if found && !row.Enabled {
		return CaptchaSkipChatDisabled, pol, nil
	}
	stored := cfg.Chats.DryRunDefault()
	if found {
		stored = row.DryRun
	}
	if cfg.Chats.DryRunFor(chatID, stored) {
		return CaptchaSkipDryRun, pol, nil
	}
	return "", pol, nil
}

func (c *Captcha) OnJoin(ctx context.Context, ev telegram.JoinEvent, displayName string) (CaptchaOutcome, error) {
	out, pol, err := c.gate(ev.ChatID)
	if out != "" || err != nil {
		return out, err
	}
	if ev.Restricted {
		return CaptchaSkipRestricted, nil
	}
	if c.Blocklist != nil && c.Blocklist.Listed(ev.UserID) {
		return CaptchaSkipBlocklisted, nil
	}
	passed, err := c.Store.CaptchaPassed(ev.ChatID, ev.UserID)
	if err != nil {
		return CaptchaError, err
	}
	if passed {
		return CaptchaSkipKnown, nil
	}
	trust, err := c.Store.TrustCount(ev.ChatID, ev.UserID)
	if err != nil {
		return CaptchaError, err
	}
	if trust > 0 {
		return CaptchaSkipKnown, nil
	}
	now := c.now()
	row, started, err := c.Store.BeginCaptcha(ev.ChatID, ev.UserID, now.Unix(), now.Add(pol.Timeout).Unix())
	if err != nil {
		return CaptchaError, err
	}
	if !started {
		return CaptchaSkipPending, nil
	}
	c.track(ev.ChatID, ev.UserID)
	name := strings.TrimSpace(displayName)
	c.Go(func() { c.challenge(ctx, ev, row.Attempt, pol, name) })
	return "", nil
}

func (c *Captcha) challenge(ctx context.Context, ev telegram.JoinEvent, attempt int64, pol config.CaptchaPolicy, displayName string) {
	defer c.untrack(ev.ChatID, ev.UserID)
	if err := c.Port.RestrictMember(ctx, ev.ChatID, ev.UserID, telegram.Perms{CanSend: false}, 0); err != nil {
		if _, _, terr := c.Store.TransitionCaptcha(ev.ChatID, ev.UserID, attempt, []string{store.CaptchaNew}, store.CaptchaCancelled); terr != nil {
			log.Printf("captcha: %v", terr)
		}
		c.log(err)
		return
	}
	if _, ok, err := c.Store.TransitionCaptcha(ev.ChatID, ev.UserID, attempt, []string{store.CaptchaNew}, store.CaptchaChallenged); err != nil || !ok {
		if err != nil {
			c.log(err)
		}
		return
	}
	buttons := [][]telegram.Button{{{Text: pol.ButtonText, Data: captchaData(ev.ChatID, ev.UserID, attempt)}}}
	ephID, err := c.Port.SendCaptchaEphemeral(ctx, ev.ChatID, ev.UserID, pol.Text, buttons)
	var msgID int
	if err != nil {
		text := pol.Text
		if displayName != "" {
			text = displayName + ", " + pol.Text
		}
		msgID, err = c.Port.SendCaptchaMessage(ctx, ev.ChatID, text, buttons)
		ephID = 0
	}
	if err != nil {
		log.Printf("captcha: %v", err)
		failed, ok, ferr := c.Store.FailCaptcha(ev.ChatID, ev.UserID, attempt, []string{store.CaptchaChallenged}, "unrestrict")
		if ferr != nil {
			c.log(ferr)
			return
		}
		if ok {
			c.fail(ctx, failed)
		}
		return
	}
	saved, err := c.Store.SetCaptchaPrompt(ev.ChatID, ev.UserID, attempt, ephID, msgID)
	if err != nil || saved.Attempt != attempt || saved.State != store.CaptchaChallenged {
		if err != nil {
			log.Printf("captcha: %v", err)
		}
		c.deleteIDs(ctx, ev.ChatID, ev.UserID, ephID, msgID)
	}
}

func (c *Captcha) OnMemberChange(ctx context.Context, ch telegram.MemberChange) (CaptchaOutcome, error) {
	row, found, err := c.Store.GetCaptcha(ch.ChatID, ch.UserID)
	if err != nil {
		return CaptchaError, err
	}
	if !found || (row.State != store.CaptchaNew && row.State != store.CaptchaChallenged) {
		return CaptchaSkip, nil
	}
	if ch.ActorID == c.SelfID || ch.ActorID == ch.UserID {
		return CaptchaSkip, nil
	}
	updated, ok, err := c.Store.TransitionCaptcha(ch.ChatID, ch.UserID, row.Attempt, []string{store.CaptchaNew, store.CaptchaChallenged}, store.CaptchaCancelled)
	if err != nil {
		return CaptchaError, err
	}
	if !ok {
		return CaptchaSkip, nil
	}
	c.deleteIDs(ctx, updated.ChatID, updated.UserID, updated.EphemeralID, updated.MessageID)
	return CaptchaCancelledByAdmin, nil
}

func (c *Captcha) OnPress(ctx context.Context, p CaptchaPress) (CaptchaOutcome, error) {
	out, err := c.decidePress(ctx, p)
	if aerr := c.Port.AnswerCallback(ctx, p.ID, captchaAnswer(out)); aerr != nil {
		log.Printf("captcha: answer: %v", aerr)
	}
	c.count(out)
	return out, err
}

func captchaAnswer(out CaptchaOutcome) string {
	switch out {
	case CaptchaWrongUser:
		return "not for you"
	case CaptchaCancelledSanction:
		return "moderators will review"
	case CaptchaError:
		return "try again"
	case CaptchaInvalid:
		return "invalid"
	case CaptchaExpired:
		return "expired"
	case CaptchaPassed:
		return "confirmed"
	default:
		return ""
	}
}

func (c *Captcha) decidePress(ctx context.Context, p CaptchaPress) (CaptchaOutcome, error) {
	chatID, userID, attempt, ok := parseCaptchaData(p.Data)
	if !ok {
		return CaptchaInvalid, nil
	}
	if p.PresserID != userID {
		return CaptchaWrongUser, nil
	}
	row, found, err := c.Store.GetCaptcha(chatID, userID)
	if err != nil {
		return CaptchaError, err
	}
	if !found || row.Attempt != attempt || row.State != store.CaptchaChallenged || row.Deadline <= c.now().Unix() {
		return CaptchaExpired, nil
	}
	sanctioned, err := c.Store.SanctionSince(chatID, userID, row.CreatedAt)
	if err != nil {
		return CaptchaError, err
	}
	if sanctioned {
		updated, moved, terr := c.Store.TransitionCaptcha(chatID, userID, attempt, []string{store.CaptchaChallenged}, store.CaptchaCancelled)
		if terr != nil {
			return CaptchaError, terr
		}
		if !moved {
			return CaptchaExpired, nil
		}
		c.deleteIDs(ctx, updated.ChatID, updated.UserID, updated.EphemeralID, updated.MessageID)
		return CaptchaCancelledSanction, nil
	}
	passed, moved, err := c.Store.TransitionCaptcha(chatID, userID, attempt, []string{store.CaptchaChallenged}, store.CaptchaPassed)
	if err != nil {
		return CaptchaError, err
	}
	if !moved {
		return CaptchaExpired, nil
	}
	if s, e := c.Store.SanctionSince(chatID, userID, row.CreatedAt); e != nil || s {
		back := store.CaptchaChallenged
		if s {
			back = store.CaptchaCancelled
		}
		c.Store.TransitionCaptcha(chatID, userID, attempt, []string{store.CaptchaPassed}, back)
		if s {
			c.deleteIDs(ctx, passed.ChatID, passed.UserID, passed.EphemeralID, passed.MessageID)
			return CaptchaCancelledSanction, nil
		}
		return CaptchaError, e
	}
	if err := c.Port.UnrestrictMember(ctx, chatID, userID); err != nil {
		if _, _, rerr := c.Store.TransitionCaptcha(chatID, userID, attempt, []string{store.CaptchaPassed}, store.CaptchaChallenged); rerr != nil {
			log.Printf("captcha: %v", rerr)
		}
		return CaptchaError, err
	}
	c.deleteIDs(ctx, passed.ChatID, passed.UserID, passed.EphemeralID, passed.MessageID)
	return CaptchaPassed, nil
}

func (c *Captcha) Sweep(ctx context.Context) (int, error) {
	due, err := c.Store.DueCaptchas(c.now().Unix(), 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range due {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		switch row.State {
		case store.CaptchaNew:
			if c.tracking(row.ChatID, row.UserID) {
				continue
			}
			n++
			failed, ok, ferr := c.Store.FailCaptcha(row.ChatID, row.UserID, row.Attempt, []string{store.CaptchaNew}, "unrestrict")
			if ferr != nil {
				c.log(ferr)
				continue
			}
			if ok {
				c.fail(ctx, failed)
			}
		case store.CaptchaChallenged:
			n++
			c.sweepChallenged(ctx, row)
		case store.CaptchaFailing:
			n++
			c.fail(ctx, row)
		}
	}
	return n, nil
}

func (c *Captcha) sweepChallenged(ctx context.Context, row store.CaptchaRow) {
	out, pol, err := c.gate(row.ChatID)
	if err != nil {
		c.log(err)
		return
	}
	action := pol.OnFail
	if out != "" {
		action = "unrestrict"
	} else {
		sanctioned, serr := c.Store.SanctionSince(row.ChatID, row.UserID, row.CreatedAt)
		if serr != nil {
			c.log(serr)
			return
		}
		if sanctioned {
			updated, ok, terr := c.Store.TransitionCaptcha(row.ChatID, row.UserID, row.Attempt, []string{store.CaptchaChallenged}, store.CaptchaCancelled)
			if terr != nil {
				c.log(terr)
				return
			}
			if ok {
				c.deleteIDs(ctx, updated.ChatID, updated.UserID, updated.EphemeralID, updated.MessageID)
				c.count(CaptchaCancelledSanction)
			}
			return
		}
	}
	failed, ok, ferr := c.Store.FailCaptcha(row.ChatID, row.UserID, row.Attempt, []string{store.CaptchaChallenged}, action)
	if ferr != nil {
		c.log(ferr)
		return
	}
	if !ok {
		return
	}
	c.deleteIDs(ctx, failed.ChatID, failed.UserID, failed.EphemeralID, failed.MessageID)
	c.fail(ctx, failed)
}

func (c *Captcha) fail(ctx context.Context, row store.CaptchaRow) {
	if row.FailAction != "keep_muted" && row.Tries >= captchaGiveUpTries {
		c.giveUp(row)
		return
	}
	switch row.FailAction {
	case "unrestrict":
		if err := c.Port.UnrestrictMember(ctx, row.ChatID, row.UserID); err != nil {
			c.retry(row, err)
			return
		}
		c.finishFail(row, store.CaptchaCancelled, CaptchaReleased)
	case "kick":
		if err := c.Port.BanMember(ctx, row.ChatID, row.UserID); err != nil {
			c.retry(row, err)
			return
		}
		if err := c.Port.UnbanMember(ctx, row.ChatID, row.UserID); err != nil {
			c.retry(row, err)
			return
		}
		c.finishFail(row, store.CaptchaFailed, CaptchaFailedKick)
	case "keep_muted":
		c.finishFail(row, store.CaptchaFailed, CaptchaFailedMuted)
	default:
		log.Printf("captcha: bad action %s", row.FailAction)
		c.giveUp(row)
	}
}

func (c *Captcha) finishFail(row store.CaptchaRow, to string, out CaptchaOutcome) {
	if _, ok, err := c.Store.TransitionCaptcha(row.ChatID, row.UserID, row.Attempt, []string{store.CaptchaFailing}, to); err != nil || !ok {
		log.Printf("captcha: %v", err)
	}
	c.count(out)
}

func (c *Captcha) giveUp(row store.CaptchaRow) {
	to := store.CaptchaFailed
	if row.FailAction == "unrestrict" {
		to = store.CaptchaCancelled
	}
	log.Printf("captcha: gave up chat=%d user=%d %s", row.ChatID, row.UserID, row.FailAction)
	c.finishFail(row, to, CaptchaGaveUp)
}

func (c *Captcha) retry(row store.CaptchaRow, cause error) {
	next := c.now().Add(captchaRetryDelay).Unix()
	if err := c.Store.RetryCaptcha(row.ChatID, row.UserID, row.Attempt, next); err != nil {
		log.Printf("captcha: %v", err)
	}
	log.Printf("captcha: %v", cause)
	c.count(CaptchaError)
}

func (c *Captcha) deleteIDs(ctx context.Context, chatID, userID int64, ephemeralID, messageID int) {
	if ephemeralID != 0 {
		if err := c.Port.DeleteEphemeral(ctx, chatID, userID, ephemeralID); err != nil {
			log.Printf("captcha: %v", err)
		}
	}
	if messageID != 0 {
		if err := c.Port.DeleteMessages(ctx, chatID, []int{messageID}); err != nil {
			log.Printf("captcha: %v", err)
		}
	}
}
