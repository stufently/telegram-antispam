package telegram

import "github.com/go-telegram/bot/models"

// JoinEvent is a person entering a chat, extracted from a chat_member
// update. ViaJoinRequest is Telegram's flag for an approved join request.
// The greeting does not branch on it; the flag is there for a later gate
// that may.
type JoinEvent struct {
	ChatID, UserID int64
	ViaJoinRequest bool
}

// JoinFromChatMemberUpdated reports whether u is a non-bot user becoming a
// member of the chat. Old status must be left, kicked, or restricted with
// is_member false; new status must be member, or restricted with is_member
// true. Promotions, renames, leaves, bans, and bots are not joins.
func JoinFromChatMemberUpdated(u models.ChatMemberUpdated) (JoinEvent, bool) {
	if !memberWasOut(u.OldChatMember) || !memberIsIn(u.NewChatMember) {
		return JoinEvent{}, false
	}
	user, ok := incomingUser(u.NewChatMember)
	if !ok || user.IsBot || user.ID == 0 {
		return JoinEvent{}, false
	}
	return JoinEvent{
		ChatID:         u.Chat.ID,
		UserID:         user.ID,
		ViaJoinRequest: u.ViaJoinRequest,
	}, true
}

func memberWasOut(cm models.ChatMember) bool {
	switch cm.Type {
	case models.ChatMemberTypeLeft, models.ChatMemberTypeBanned:
		return true
	case models.ChatMemberTypeRestricted:
		return cm.Restricted != nil && !cm.Restricted.IsMember
	default:
		return false
	}
}

func memberIsIn(cm models.ChatMember) bool {
	switch cm.Type {
	case models.ChatMemberTypeMember:
		return cm.Member != nil && cm.Member.User != nil
	case models.ChatMemberTypeRestricted:
		return cm.Restricted != nil && cm.Restricted.User != nil && cm.Restricted.IsMember
	default:
		return false
	}
}

func incomingUser(cm models.ChatMember) (models.User, bool) {
	switch cm.Type {
	case models.ChatMemberTypeMember:
		if cm.Member == nil || cm.Member.User == nil {
			return models.User{}, false
		}
		return *cm.Member.User, true
	case models.ChatMemberTypeRestricted:
		if cm.Restricted == nil || cm.Restricted.User == nil {
			return models.User{}, false
		}
		return *cm.Restricted.User, true
	default:
		return models.User{}, false
	}
}
