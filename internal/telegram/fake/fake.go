// Package fake is an in-memory telegram.Port for tests. It records the order
// of calls so tests can assert evidence-before-action ordering.
package fake

import (
	"context"
	"sync"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

type Fake struct {
	mu    sync.Mutex
	calls []string

	// knobs
	CopyErr error
	// CopyOmit drops that many ids from the END of what CopyMessages
	// returns, WITHOUT returning an error. That is Telegram's real
	// behaviour for a message it cannot copy (a quiz poll, say): the call
	// succeeds and the skipped message simply is not in the result. Set it
	// to len(ids) or more to get the empty result — the case where the
	// admin chat ends up with a verdict card and nothing under it.
	CopyOmit int
	// SendAdminErr fails the admin-chat card, which ends the incident before
	// any sanction: tests use it to cover that terminal branch.
	SendAdminErr error
	SendAdminID  int
	Admins       []telegram.Member
	AdminsErr    error
	// BeforeGetAdmins, when set, runs at the start of
	// GetChatAdministrators. Tests use it to hold a fetch in flight and
	// interleave other cache operations with it.
	BeforeGetAdmins func()
	BanSenderErr    error
	// UnbanErr makes the undo path fail, so tests can cover a transient
	// Telegram error during a moderator's rollback press.
	UnbanErr     error
	EphemeralID  int
	EphemeralErr error
	WelcomeID    int
	WelcomeErr   error
	Rights       telegram.BotRights
	RightsErr    error
	// Titles answers ChatTitle per chat id; TitleErr fails the lookup, which
	// the admin card must survive by falling back to the bare id.
	Titles   map[int64]string
	TitleErr error
	// DeleteErr and RestrictErr let a test drive the two halves of
	// enforcement independently: they fail separately in production (a
	// message older than 48h cannot be deleted even when the mute lands),
	// and the caller's next move differs for each.
	DeleteErr   error
	RestrictErr error

	// LastAdmin captures the most recent AdminMessage passed to SendAdmin, so
	// tests can assert on fields SendAdmin doesn't otherwise record.
	LastAdmin telegram.AdminMessage

	// LastReactionDelete captures the most recent args passed to
	// DeleteMessageReaction, so tests can assert on fields the call log
	// doesn't otherwise record.
	LastReactionDelete struct {
		Chat      int64
		MessageID int
		UserID    int64
	}

	// LastEphemeral captures the most recent args passed to SendEphemeral, so
	// tests can assert on fields the call log doesn't otherwise record.
	LastEphemeral struct {
		Chat, UserID int64
		Text         string
	}

	// LastWelcome captures the most recent args passed to SendWelcome.
	LastWelcome struct {
		Chat, UserID int64
		Text         string
	}

	// LastUnban, LastRestrict, and LastDelete capture the most recent args
	// of the calls the admin-chat undo path makes, so tests can assert that
	// a button lifted the right sanction for the right user (the call log
	// alone cannot tell "unmuted user 7" from "unmuted somebody").
	LastUnban struct {
		Chat, UserID int64
	}
	LastUnrestrict struct {
		Chat, UserID int64
	}
	LastRestrict struct {
		Chat, UserID int64
		Perms        telegram.Perms
		Until        int64
	}
	LastDelete struct {
		Chat int64
		IDs  []int
	}
}

func New() *Fake { return &Fake{} }

func (f *Fake) log(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

// Calls returns the recorded call names in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *Fake) CopyMessages(_ context.Context, _, _ int64, ids []int) ([]int, error) {
	f.log("CopyMessages")
	if f.CopyErr != nil {
		return nil, f.CopyErr
	}
	n := min(max(len(ids)-f.CopyOmit, 0), len(ids))
	out := make([]int, 0, n)
	for _, id := range ids[:n] {
		out = append(out, 100000+id)
	}
	return out, nil
}

func (f *Fake) DeleteMessages(_ context.Context, chat int64, ids []int) error {
	f.mu.Lock()
	f.LastDelete.Chat, f.LastDelete.IDs = chat, ids
	f.mu.Unlock()
	f.log("DeleteMessages")
	return f.DeleteErr
}

func (f *Fake) BanMember(_ context.Context, _, _ int64) error {
	f.log("BanMember")
	return nil
}

func (f *Fake) UnbanMember(_ context.Context, chat, user int64) error {
	f.mu.Lock()
	f.LastUnban.Chat, f.LastUnban.UserID = chat, user
	f.mu.Unlock()
	f.log("UnbanMember")
	return f.UnbanErr
}

func (f *Fake) UnrestrictMember(_ context.Context, chat, user int64) error {
	f.mu.Lock()
	f.LastUnrestrict.Chat, f.LastUnrestrict.UserID = chat, user
	f.mu.Unlock()
	f.log("UnrestrictMember")
	return nil
}

func (f *Fake) RestrictMember(_ context.Context, chat, user int64, perms telegram.Perms, until int64) error {
	f.mu.Lock()
	f.LastRestrict.Chat, f.LastRestrict.UserID = chat, user
	f.LastRestrict.Perms, f.LastRestrict.Until = perms, until
	f.mu.Unlock()
	f.log("RestrictMember")
	return f.RestrictErr
}

func (f *Fake) SendAdmin(_ context.Context, _ int64, msg telegram.AdminMessage) (int, error) {
	f.mu.Lock()
	f.LastAdmin = msg
	f.mu.Unlock()
	f.log("SendAdmin")
	return f.SendAdminID, f.SendAdminErr
}

func (f *Fake) BanSenderChat(_ context.Context, _, _ int64) error {
	f.log("BanSenderChat")
	return f.BanSenderErr
}

func (f *Fake) UnbanSenderChat(_ context.Context, _, _ int64) error {
	f.log("UnbanSenderChat")
	return nil
}

func (f *Fake) GetChatAdministrators(ctx context.Context, _ int64) ([]telegram.Member, error) {
	f.log("GetChatAdministrators")
	if f.BeforeGetAdmins != nil {
		f.BeforeGetAdmins()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.AdminsErr != nil {
		return nil, f.AdminsErr
	}
	return f.Admins, nil
}

// ChatTitle returns Titles[chat], or TitleErr when set. An unknown chat
// answers the empty string with no error, which is the "chat has no title"
// case the card has to render as an id.
func (f *Fake) ChatTitle(_ context.Context, chat int64) (string, error) {
	f.log("ChatTitle")
	if f.TitleErr != nil {
		return "", f.TitleErr
	}
	return f.Titles[chat], nil
}

func (f *Fake) AnswerCallback(_ context.Context, _, _ string) error {
	f.log("AnswerCallback")
	return nil
}

func (f *Fake) EditAdminMarkup(_ context.Context, _ int64, _ int, _ [][]telegram.Button) error {
	f.log("EditAdminMarkup")
	return nil
}

func (f *Fake) DeleteMessageReaction(_ context.Context, chat int64, messageID int, userID int64) error {
	f.mu.Lock()
	f.LastReactionDelete.Chat = chat
	f.LastReactionDelete.MessageID = messageID
	f.LastReactionDelete.UserID = userID
	f.mu.Unlock()
	f.log("DeleteMessageReaction")
	return nil
}

func (f *Fake) SendEphemeral(_ context.Context, chat, userID int64, text string) (int, error) {
	f.mu.Lock()
	f.LastEphemeral.Chat = chat
	f.LastEphemeral.UserID = userID
	f.LastEphemeral.Text = text
	f.mu.Unlock()
	f.log("SendEphemeral")
	return f.EphemeralID, f.EphemeralErr
}

func (f *Fake) SendWelcome(_ context.Context, chat, userID int64, text string) (int, error) {
	f.mu.Lock()
	f.LastWelcome.Chat = chat
	f.LastWelcome.UserID = userID
	f.LastWelcome.Text = text
	f.mu.Unlock()
	f.log("SendWelcome")
	return f.WelcomeID, f.WelcomeErr
}

func (f *Fake) CheckBotRights(_ context.Context, _ int64) (telegram.BotRights, error) {
	f.log("CheckBotRights")
	return f.Rights, f.RightsErr
}
