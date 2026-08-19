// Package selfcheck verifies, at startup and on my_chat_member updates, that
// the bot holds the admin rights it needs in each chat and warns when a chat
// has native Aggressive Anti-Spam enabled (spec §13). It is a wiring helper,
// not part of the pure detection core: it depends on the telegram Port to
// read the bot's rights.
package selfcheck

import (
	"context"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

// RightsChecker is the narrow slice of telegram.Port this package needs.
type RightsChecker interface {
	CheckBotRights(ctx context.Context, chat int64) (telegram.BotRights, error)
}

// Warnings returns human-readable problems implied by the bot's rights in a
// chat. An empty result means the bot can moderate and no hazard was found.
// A non-admin bot short-circuits: the missing per-right flags are implied by
// "not an administrator", so listing them too would be noise.
func Warnings(r telegram.BotRights) []string {
	if !r.IsAdmin {
		msgs := []string{"bot is not an administrator — it cannot delete messages or restrict members"}
		if r.AggressiveAntiSpam {
			msgs = append(msgs, aggressiveWarn)
		}
		return msgs
	}
	var msgs []string
	if !r.CanDelete {
		msgs = append(msgs, "missing can_delete_messages")
	}
	if !r.CanRestrict {
		msgs = append(msgs, "missing can_restrict_members")
	}
	if r.AggressiveAntiSpam {
		msgs = append(msgs, aggressiveWarn)
	}
	return msgs
}

const aggressiveWarn = "native Aggressive Anti-Spam is enabled — Telegram may delete messages before the bot sees them"

// Check reads the bot's rights in one chat and returns any warnings. An error
// from the checker is returned as-is (the caller decides whether a transient
// read failure is worth logging).
func Check(ctx context.Context, rc RightsChecker, chat int64) ([]string, error) {
	r, err := rc.CheckBotRights(ctx, chat)
	if err != nil {
		return nil, err
	}
	return Warnings(r), nil
}
