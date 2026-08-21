// Package detect holds pure detection logic: no side effects, no Telegram
// library types. This file classifies the four sender identities (spec §4).
package detect

import "github.com/stufently/telegram-antispam/internal/domain"

const (
	AnonAdminBotID         int64 = 1087968824
	ChannelBotID           int64 = 136817688
	ServiceNotificationsID int64 = 777000
)

// ClassifyInput is the raw sender data the adapter extracts from an update.
type ClassifyInput struct {
	FromID             int64
	IsBot              bool
	SenderChatID       int64
	SenderChatType     string
	ChatID             int64
	LinkedChatID       int64
	IsAutomaticForward bool
}

// ClassifySender maps raw sender data to a SenderKind. Order matters: the
// anonymous-admin and linked-channel checks must precede the generic
// external-channel case.
func ClassifySender(in ClassifyInput) domain.SenderKind {
	if in.SenderChatID != 0 {
		switch {
		case in.SenderChatID == in.ChatID || in.FromID == AnonAdminBotID:
			return domain.SenderAnonAdmin
		// is_automatic_forward is set by Telegram ONLY on a channel post the
		// server itself copied into the discussion group linked to that
		// channel — a user cannot forge it on an ordinary message. So it is
		// sufficient evidence on its own, and the LinkedChatID comparison is
		// applied only when the caller actually knows the linked chat.
		//
		// It must work without it: nothing populates LinkedChatID (the Bot
		// API does not put it on a message; it takes a separate getChat), so
		// requiring the match classified every routine auto-post of a chat's
		// OWN channel as an external channel — stripping the immunity that
		// ImmuneSender grants linked channels and raising incidents against
		// the chat's own announcements.
		case in.IsAutomaticForward && (in.LinkedChatID == 0 || in.SenderChatID == in.LinkedChatID):
			return domain.SenderLinkedChannel
		default:
			return domain.SenderExternalChannel
		}
	}
	if in.IsBot {
		return domain.SenderBot
	}
	return domain.SenderUser
}
