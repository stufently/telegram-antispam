// Package ops (this file) provides the daily digest: a 24h summary of
// moderation actions sent to the admin chat.
package ops

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

// AdminSender is the narrow Telegram surface the digest needs to deliver its
// summary. Satisfied by telegram.Port.
type AdminSender interface {
	SendAdmin(ctx context.Context, adminChat int64, msg telegram.AdminMessage) (int, error)
}

// DigestSource is the narrow store surface the digest needs to compute its
// summary. Satisfied by *store.DB.
type DigestSource interface {
	ActionCountsSince(ts int64) (map[string]int, error)
}

// BuildDigest formats a compact human summary of action counts. Deterministic:
// actions are sorted alphabetically.
func BuildDigest(counts map[string]int, sinceHuman string) string {
	if len(counts) == 0 {
		return fmt.Sprintf("Daily digest (%s): no incidents.", sinceHuman)
	}

	actions := make([]string, 0, len(counts))
	for action := range counts {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	parts := make([]string, 0, len(actions))
	total := 0
	for _, action := range actions {
		n := counts[action]
		total += n
		parts = append(parts, fmt.Sprintf("%s %d", action, n))
	}

	return fmt.Sprintf("Daily digest (%s): %s — total %d", sinceHuman, strings.Join(parts, ", "), total)
}

// SendDigest computes the 24h window ending at now, fetches action counts
// from src, builds the summary text, and sends it via sender. The caller
// decides how to treat a returned error (e.g. log-and-continue for
// best-effort delivery).
func SendDigest(ctx context.Context, sender AdminSender, adminChat int64, src DigestSource, now int64) error {
	since := now - 86400
	counts, err := src.ActionCountsSince(since)
	if err != nil {
		return err
	}
	text := BuildDigest(counts, "last 24h")
	_, err = sender.SendAdmin(ctx, adminChat, telegram.AdminMessage{Text: text})
	return err
}
