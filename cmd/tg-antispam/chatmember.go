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
		if !isJoin || welcomer == nil {
			return
		}
		outcome, err := welcomer.Observe(ctx, join)
		if count != nil {
			count(string(outcome))
		}
		if err != nil {
			log.Printf("welcome chat=%d user=%d: %v", join.ChatID, join.UserID, err)
		}
	})
}
