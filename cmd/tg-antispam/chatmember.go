package main

import (
	"context"
	"log"

	"github.com/go-telegram/bot/models"

	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/watch"
)

// handleChatMember is the chat_member branch of the update handler.
//
// Invalidation stays on the inline consumer, before the job is queued, so
// a later update from this chat cannot observe a stale admin list.
// Identity recording and the welcome run in the same per-chat job, welcome
// strictly after the identity write: the sequencer is what keeps those two
// in order when several membership updates arrive together.
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
	// Only a change that touches the admin roster invalidates it.
	// chat_member also fires for every ordinary join, leave, and
	// restriction, and dropping the cache on those would turn the
	// TTL cache into a per-event GetChatAdministrators during a
	// raid — and stretch the windows where a failing lookup has
	// nothing cached to fall back on. Invalidate on the inline
	// consumer, before later updates from this chat can be
	// submitted, then let the sequenced watcher refetch as needed.
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
