package main

import (
	"context"
	"log"

	"github.com/go-telegram/bot/models"

	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/watch"
)

// handleChatMember invalidates the admin cache on the inline consumer, then
// records the identity and maybe greets, in that order, inside one per-chat job.
func handleChatMember(
	ctx context.Context,
	cm *models.ChatMemberUpdated,
	invalidate func(chatID int64),
	submit func(chatID int64, job func()),
	members *watch.MemberWatcher,
	captcha *watch.Captcha,
	welcomer *watch.Welcomer,
	count func(result string),
) {
	if cm == nil {
		return
	}
	mem := telegram.MemberFromChatMember(cm.NewChatMember)
	// Ordinary joins must not drop the admin cache: during a raid that
	// would turn it into a GetChatAdministrators per event. Invalidate
	// here, on the inline consumer, before this chat's next update is queued.
	if invalidate != nil && (isAdminStatus(telegram.MemberFromChatMember(cm.OldChatMember).Status) || isAdminStatus(mem.Status)) {
		invalidate(cm.Chat.ID)
	}
	ev := watch.MemberEvent{
		ChatID:      cm.Chat.ID,
		UserID:      mem.UserID,
		Username:    mem.Username,
		DisplayName: mem.DisplayName,
	}
	join, isJoin := telegram.JoinFromChatMemberUpdated(*cm)
	submit(cm.Chat.ID, func() {
		if members != nil {
			if err := members.Observe(ctx, ev); err != nil {
				log.Printf("member watch: %v", err)
			}
		}
		if captcha != nil {
			if ch, ok := telegram.MemberChangeFromUpdate(*cm); ok {
				out, err := captcha.OnMemberChange(ctx, ch)
				noteCaptcha(captcha, out, err)
			}
			if isJoin {
				out, err := captcha.OnJoin(ctx, join, mem.DisplayName)
				noteCaptcha(captcha, out, err)
			}
		}
		if !isJoin || welcomer == nil {
			return
		}
		outcome, ticket, err := welcomer.Admit(ctx, join)
		if outcome != watch.OutcomeQueued {
			if count != nil {
				count(string(outcome))
			}
			if err != nil {
				log.Printf("welcome chat=%d user=%d: %v", join.ChatID, join.UserID, err)
			}
			return
		}
		welcomer.DeliverAsync(ctx, ticket, func(outcome watch.Outcome, err error) {
			if count != nil {
				count(string(outcome))
			}
			if err != nil {
				log.Printf("welcome chat=%d user=%d: %v", join.ChatID, join.UserID, err)
			}
		})
	})
}

// handleChatJoinRequest counts the update, then runs the captcha decision
// inside the chat's sequencer job. A bot or a request with nowhere to write
// is counted and dropped.
func handleChatJoinRequest(
	ctx context.Context,
	req *models.ChatJoinRequest,
	submit func(chatID int64, job func()),
	captcha *watch.Captcha,
	count func(kind string),
) {
	if req == nil {
		return
	}
	if count != nil {
		count("chat_join_request")
	}
	jr, ok := telegram.JoinRequestFromUpdate(*req)
	if !ok || submit == nil {
		return
	}
	submit(jr.ChatID, func() {
		if captcha == nil {
			return
		}
		out, err := captcha.OnJoinRequest(ctx, jr)
		noteCaptcha(captcha, out, err)
	})
}

func noteCaptcha(c *watch.Captcha, out watch.CaptchaOutcome, err error) {
	if c.Count != nil && out != "" && out != watch.CaptchaSkip {
		c.Count(string(out))
	}
	if err != nil {
		log.Printf("captcha: %v", err)
	}
}

func dispatchCallback(data string, onCaptcha, onAdmin func()) {
	if watch.IsCaptchaCallback(data) {
		if onCaptcha != nil {
			onCaptcha()
		}
		return
	}
	if onAdmin != nil {
		onAdmin()
	}
}
