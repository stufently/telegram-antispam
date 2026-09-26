package telegram

import "github.com/go-telegram/bot/models"

// JoinEvent is one person entering a chat. ViaJoinRequest is recorded and
// does not change the greeting.
type JoinEvent struct {
	ChatID, UserID int64
	ViaJoinRequest bool
	Restricted     bool
}

type MemberChange struct {
	ChatID, UserID, ActorID int64
}

// JoinRequest is one chat_join_request. UserChatID is the private chat
// Telegram opens with the applicant for five minutes.
type JoinRequest struct {
	ChatID, UserID, UserChatID int64
	DisplayName                string
}

// JoinRequestFromUpdate is false for a bot, a zero user id, or a zero
// private-chat id: there is nobody to prompt, or nowhere to send the button.
func JoinRequestFromUpdate(r models.ChatJoinRequest) (JoinRequest, bool) {
	if r.From.IsBot || r.From.ID == 0 || r.UserChatID == 0 {
		return JoinRequest{}, false
	}
	return JoinRequest{
		ChatID:      r.Chat.ID,
		UserID:      r.From.ID,
		UserChatID:  r.UserChatID,
		DisplayName: r.From.FirstName,
	}, true
}

// JoinFromChatMemberUpdated is true only for a non-bot user moving from
// left, kicked, or restricted with is_member false into member, or into
// restricted with is_member true. Promotions, renames, leaves, bans and
// bots are not joins.
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
		Restricted:     u.NewChatMember.Type == models.ChatMemberTypeRestricted,
	}, true
}

func MemberChangeFromUpdate(u models.ChatMemberUpdated) (MemberChange, bool) {
	mem := MemberFromChatMember(u.NewChatMember)
	if mem.UserID == 0 {
		return MemberChange{}, false
	}
	return MemberChange{ChatID: u.Chat.ID, UserID: mem.UserID, ActorID: u.From.ID}, true
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
