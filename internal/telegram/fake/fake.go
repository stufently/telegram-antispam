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
	CopyErr      error
	SendAdminID  int
	Admins       []telegram.Member
	AdminsErr    error
	BanSenderErr error
	EphemeralID  int
	EphemeralErr error
	Rights       telegram.BotRights
	RightsErr    error

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
	out := make([]int, len(ids))
	for i := range ids {
		out[i] = 100000 + ids[i]
	}
	return out, nil
}

func (f *Fake) DeleteMessages(_ context.Context, _ int64, _ []int) error {
	f.log("DeleteMessages")
	return nil
}

func (f *Fake) BanMember(_ context.Context, _, _ int64) error {
	f.log("BanMember")
	return nil
}

func (f *Fake) RestrictMember(_ context.Context, _, _ int64, _ telegram.Perms, _ int64) error {
	f.log("RestrictMember")
	return nil
}

func (f *Fake) SendAdmin(_ context.Context, _ int64, msg telegram.AdminMessage) (int, error) {
	f.mu.Lock()
	f.LastAdmin = msg
	f.mu.Unlock()
	f.log("SendAdmin")
	return f.SendAdminID, nil
}

func (f *Fake) BanSenderChat(_ context.Context, _, _ int64) error {
	f.log("BanSenderChat")
	return f.BanSenderErr
}

func (f *Fake) GetChatAdministrators(_ context.Context, _ int64) ([]telegram.Member, error) {
	f.log("GetChatAdministrators")
	if f.AdminsErr != nil {
		return nil, f.AdminsErr
	}
	return f.Admins, nil
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

func (f *Fake) CheckBotRights(_ context.Context, _ int64) (telegram.BotRights, error) {
	f.log("CheckBotRights")
	return f.Rights, f.RightsErr
}
