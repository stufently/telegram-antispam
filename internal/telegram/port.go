// Package telegram is the only package that talks to the Telegram library.
// The rest of the bot depends on Port so it can be tested with a fake.
package telegram

import (
	"context"
	"errors"
)

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
	// CopyMessageIDs are the admin-chat ids of the copied evidence. The card
	// is sent as a reply to the first of them, which is what tells a reviewer
	// WHICH evidence a verdict belongs to: incidents from different chats run
	// in parallel, so admin-chat order alone does not pair them. Empty means
	// the copy failed and the card goes out unthreaded.
	CopyMessageIDs []int
	Buttons        [][]Button
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
	// CopyMessages returns the ids of the copies that were actually made,
	// which may be FEWER than the ids asked for — and, for a single
	// message, none at all. Telegram's copyMessages silently skips what it
	// cannot copy (a quiz poll is the case seen in production, and a poll's
	// option texts are part of what the detectors judge) and reports success
	// anyway. Callers must therefore compare len(result) with len(ids)
	// rather than read a nil error as "the evidence is in the admin chat".
	//
	// The result is a list of DESTINATION ids in the admin chat with no
	// mapping back to the sources, so a short list says how many parts are
	// missing and never which ones.
	CopyMessages(ctx context.Context, dstChat, srcChat int64, ids []int) ([]int, error)
	DeleteMessages(ctx context.Context, chat int64, ids []int) error
	BanMember(ctx context.Context, chat, user int64) error
	UnbanMember(ctx context.Context, chat, user int64) error
	UnrestrictMember(ctx context.Context, chat, user int64) error
	RestrictMember(ctx context.Context, chat, user int64, perms Perms, until int64) error
	SendAdmin(ctx context.Context, adminChat int64, msg AdminMessage) (int, error)
	BanSenderChat(ctx context.Context, chat, senderChat int64) error
	// UnbanSenderChat lifts a BanSenderChat. It exists for the same reason
	// UnbanMember does: a sanction the admin-chat buttons cannot reverse is
	// worse than no button at all.
	UnbanSenderChat(ctx context.Context, chat, senderChat int64) error
	GetChatAdministrators(ctx context.Context, chat int64) ([]Member, error)
	// ChatTitle names a chat for a human reading the admin chat. An evidence
	// copy carries no origin — copyMessage strips it, which is the point —
	// so without this a moderator sees spam and cannot tell WHERE it was
	// posted. Best-effort by contract: an error means "show the id".
	ChatTitle(ctx context.Context, chat int64) (string, error)
	AnswerCallback(ctx context.Context, callbackID, text string) error
	EditAdminMarkup(ctx context.Context, adminChat int64, messageID int, buttons [][]Button) error
	DeleteMessageReaction(ctx context.Context, chat int64, messageID int, userID int64) error
	SendEphemeral(ctx context.Context, chat, userID int64, text string) (int, error)
	// SendWelcome is the same ephemeral send, under its own priority name.
	SendWelcome(ctx context.Context, chat, userID int64, text string) (int, error)
	SendCaptchaEphemeral(ctx context.Context, chat, userID int64, text string, buttons [][]Button) (int, error)
	SendCaptchaMessage(ctx context.Context, chat int64, text string, buttons [][]Button) (int, error)
	// ApproveJoinRequest and DeclineJoinRequest settle a chat_join_request.
	// They run at the default queue priority: they are not a mute or a ban.
	ApproveJoinRequest(ctx context.Context, chat, user int64) error
	DeclineJoinRequest(ctx context.Context, chat, user int64) error
	DeleteEphemeral(ctx context.Context, chat, userID int64, ephemeralID int) error
	CheckBotRights(ctx context.Context, chat int64) (BotRights, error)
}

// ErrEphemeralNotHonored means Telegram published the text (message_id set,
// ephemeral_message_id absent). The port deletes that message first. A
// delete failure is wrapped in this error.
var ErrEphemeralNotHonored = errors.New("telegram: ephemeral send was published to the chat")
