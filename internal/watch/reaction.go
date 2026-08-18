package watch

import (
	"context"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

// SpammerSource reports whether a user is a known spammer in a chat
// (consumer-declared; *store.DB satisfies it).
type SpammerSource interface {
	HasSpamIncident(chatID, userID int64) (bool, error)
}

// ReactionEvent is the library-free shape the wiring extracts from a
// MessageReactionUpdated. Added is true when the user placed/added a
// reaction (new reaction set larger than old), not removed one.
type ReactionEvent struct {
	ChatID    int64
	MessageID int
	UserID    int64
	Added     bool
}

// ReactionCleaner removes reactions added by users with a known spam
// incident in the chat.
type ReactionCleaner struct {
	Spammers SpammerSource
	Port     telegram.Port
	Enabled  bool
}

// Observe inspects a reaction update and, if it's a newly-added reaction
// from a known spammer, removes it. Non-added reactions, disabled state,
// and reactions from unknown users are left untouched.
func (r *ReactionCleaner) Observe(ctx context.Context, e ReactionEvent) error {
	if !r.Enabled || !e.Added || e.UserID == 0 {
		return nil
	}

	known, err := r.Spammers.HasSpamIncident(e.ChatID, e.UserID)
	if err != nil {
		return err
	}

	if !known {
		return nil
	}

	return r.Port.DeleteMessageReaction(ctx, e.ChatID, e.MessageID, e.UserID)
}
