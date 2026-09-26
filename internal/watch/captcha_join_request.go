package watch

import (
	"context"
	"log"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// OnJoinRequest decides a chat_join_request. The network (approve, or the
// private button) runs in Go so the per-chat job does not wait on Telegram.
func (c *Captcha) OnJoinRequest(ctx context.Context, r telegram.JoinRequest) (CaptchaOutcome, error) {
	out, pol, err := c.gate(r.ChatID)
	if out != "" || err != nil {
		return out, err
	}
	if pol.Mode != "join_request" {
		return CaptchaSkipMode, nil
	}
	if c.Blocklist != nil && c.Blocklist.Listed(r.UserID) {
		return CaptchaSkipBlocklisted, nil
	}
	passed, err := c.Store.CaptchaPassed(r.ChatID, r.UserID)
	if err != nil {
		return CaptchaError, err
	}
	trust, err := c.Store.TrustCount(r.ChatID, r.UserID)
	if err != nil {
		return CaptchaError, err
	}
	if passed || trust > 0 {
		c.Go(func() {
			if aerr := c.Port.ApproveJoinRequest(ctx, r.ChatID, r.UserID); aerr != nil {
				c.log(aerr)
			}
		})
		return CaptchaApprovedKnown, nil
	}
	now := c.now()
	row, started, err := c.Store.BeginCaptcha(r.ChatID, r.UserID, now.Unix(), now.Add(pol.Timeout).Unix(), "join_request")
	if err != nil {
		return CaptchaError, err
	}
	if !started {
		return CaptchaSkipPending, nil
	}
	c.track(r.ChatID, r.UserID)
	c.Go(func() { c.challengeJoin(ctx, r, row.Attempt, pol) })
	return CaptchaChallenged, nil
}

func (c *Captcha) challengeJoin(ctx context.Context, r telegram.JoinRequest, attempt int64, pol config.CaptchaPolicy) {
	defer c.untrack(r.ChatID, r.UserID)
	if _, ok, err := c.Store.TransitionCaptcha(r.ChatID, r.UserID, attempt, []string{store.CaptchaNew}, store.CaptchaChallenged); err != nil || !ok {
		if err != nil {
			c.log(err)
		}
		return
	}
	buttons := [][]telegram.Button{{{Text: pol.ButtonText, Data: captchaData(r.ChatID, r.UserID, attempt)}}}
	msgID, err := c.Port.SendCaptchaMessage(ctx, r.UserChatID, pol.Text, buttons)
	if err != nil {
		if _, _, terr := c.Store.TransitionCaptcha(r.ChatID, r.UserID, attempt, []string{store.CaptchaChallenged}, store.CaptchaCancelled); terr != nil {
			c.log(terr)
		}
		log.Printf("captcha: %v", err)
		c.count(CaptchaPromptFailed)
		return
	}
	sentAt := c.now()
	saved, err := c.Store.SetCaptchaPrompt(r.ChatID, r.UserID, attempt, r.UserChatID, 0, msgID, sentAt.Add(pol.Timeout).Unix())
	if err != nil {
		log.Printf("captcha: %v", err)
		if _, _, terr := c.Store.TransitionCaptcha(r.ChatID, r.UserID, attempt, []string{store.CaptchaChallenged}, store.CaptchaCancelled); terr != nil {
			c.log(terr)
		}
		c.deleteIDs(ctx, store.CaptchaRow{ChatID: r.ChatID, UserID: r.UserID, PromptChatID: r.UserChatID, MessageID: msgID})
		c.count(CaptchaPromptFailed)
		return
	}
	if saved.Attempt != attempt || saved.State != store.CaptchaChallenged {
		c.deleteIDs(ctx, store.CaptchaRow{ChatID: r.ChatID, UserID: r.UserID, PromptChatID: r.UserChatID, MessageID: msgID})
	}
}

func (c *Captcha) cancelJoin(ctx context.Context, row store.CaptchaRow, from string, drop bool) {
	updated, ok, err := c.Store.TransitionCaptcha(row.ChatID, row.UserID, row.Attempt, []string{from}, store.CaptchaCancelled)
	if err != nil {
		c.log(err)
		return
	}
	if !ok {
		return
	}
	if drop {
		c.deleteIDs(ctx, updated)
	}
	c.count(CaptchaLeftPending)
}

func (c *Captcha) sweepJoinChallenged(ctx context.Context, row store.CaptchaRow) {
	out, pol, err := c.gate(row.ChatID)
	if err != nil {
		c.log(err)
		return
	}
	// challenged is recorded before the private button exists. A deadline
	// that arrives with no message was a crash or a send that never
	// landed; declining it would punish someone who was never asked.
	if out != "" || pol.OnFail != "kick" || row.MessageID == 0 {
		c.cancelJoin(ctx, row, store.CaptchaChallenged, row.MessageID != 0)
		return
	}
	failed, ok, ferr := c.Store.FailCaptcha(row.ChatID, row.UserID, row.Attempt, []string{store.CaptchaChallenged}, "decline")
	if ferr != nil {
		c.log(ferr)
		return
	}
	if !ok {
		return
	}
	c.deleteIDs(ctx, failed)
	c.fail(ctx, failed)
}

func (c *Captcha) sweepJoinPassing(ctx context.Context, row store.CaptchaRow) {
	if row.Tries >= captchaGiveUpTries {
		log.Printf("captcha: gave up chat=%d user=%d passing", row.ChatID, row.UserID)
		if _, ok, terr := c.Store.TransitionCaptcha(row.ChatID, row.UserID, row.Attempt, []string{store.CaptchaPassing}, store.CaptchaCancelled); terr != nil {
			c.log(terr)
			return
		} else if ok {
			c.deleteIDs(ctx, row)
			c.count(CaptchaGaveUp)
		}
		return
	}
	if err := c.Port.ApproveJoinRequest(ctx, row.ChatID, row.UserID); err != nil {
		c.retry(row, err)
		return
	}
	updated, ok, terr := c.Store.TransitionCaptcha(row.ChatID, row.UserID, row.Attempt, []string{store.CaptchaPassing}, store.CaptchaPassed)
	if terr != nil {
		c.log(terr)
		return
	}
	if !ok {
		return
	}
	c.deleteIDs(ctx, updated)
	c.count(CaptchaApproved)
}
