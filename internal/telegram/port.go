// Package telegram is the only package that talks to the Telegram library.
// The rest of the bot depends on Port so it can be tested with a fake.
package telegram

import "context"

// Perms is the subset of chat permissions the bot toggles when muting.
type Perms struct {
	CanSend bool
}

// Member is a chat member as the bot needs it.
type Member struct {
	UserID      int64
	Status      string
	Username    string
	DisplayName string
	CustomTitle string
}

// AdminMessage is a summary sent to the admin chat alongside copied evidence.
type AdminMessage struct {
	Text             string
	IncidentKey      string
	SourceChatID     int64
	CopiedFromChatID int64
	CopyMessageIDs   []int
	Buttons          [][]Button
}

// Button is one inline keyboard button (text + opaque callback data ≤64 bytes).
type Button struct {
	Text string
	Data string
}

// BotRights is the subset of the bot's own permissions in a chat that the
// startup/my_chat_member self-check inspects (spec §13). AggressiveAntiSpam
// mirrors the chat's native anti-spam toggle, which — when on — deletes
// messages before the bot can see them.
type BotRights struct {
	IsAdmin            bool
	CanDelete          bool
	CanRestrict        bool
	AggressiveAntiSpam bool
}

// Port is the narrow Telegram surface the incident logic depends on.
type Port interface {
	CopyMessages(ctx context.Context, dstChat, srcChat int64, ids []int) ([]int, error)
	DeleteMessages(ctx context.Context, chat int64, ids []int) error
	BanMember(ctx context.Context, chat, user int64) error
	UnbanMember(ctx context.Context, chat, user int64) error
	RestrictMember(ctx context.Context, chat, user int64, perms Perms, until int64) error
	SendAdmin(ctx context.Context, adminChat int64, msg AdminMessage) (int, error)
	BanSenderChat(ctx context.Context, chat, senderChat int64) error
	GetChatAdministrators(ctx context.Context, chat int64) ([]Member, error)
	AnswerCallback(ctx context.Context, callbackID, text string) error
	EditAdminMarkup(ctx context.Context, adminChat int64, messageID int, buttons [][]Button) error
	DeleteMessageReaction(ctx context.Context, chat int64, messageID int, userID int64) error
	SendEphemeral(ctx context.Context, chat, userID int64, text string) (int, error)
	CheckBotRights(ctx context.Context, chat int64) (BotRights, error)
}
