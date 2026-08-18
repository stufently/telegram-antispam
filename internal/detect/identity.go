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
		case in.IsAutomaticForward && in.SenderChatID == in.LinkedChatID:
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
