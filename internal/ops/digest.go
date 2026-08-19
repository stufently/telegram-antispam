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
	ActionCountsSince(ts int64) (applied, dryRun, incomplete map[string]int, err error)
}

// BuildDigest formats a compact human summary of action counts.
// Deterministic: actions are sorted alphabetically.
//
// The three groups are reported separately and never summed together. An
// audit row records a verdict, not an outcome: a dry-run chat produces rows
// for actions that were deliberately never carried out, and a live incident
// can fail before it acts. Folding either into the headline count would tell
// an operator that bans happened when none did.
func BuildDigest(applied, dryRun, incomplete map[string]int, sinceHuman string) string {
	if len(applied) == 0 && len(dryRun) == 0 && len(incomplete) == 0 {
		return fmt.Sprintf("Daily digest (%s): no incidents.", sinceHuman)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Daily digest (%s): ", sinceHuman)
	if len(applied) == 0 {
		sb.WriteString("no actions applied")
	} else {
		parts, total := formatCounts(applied)
		fmt.Fprintf(&sb, "%s — total %d", parts, total)
	}
	if len(dryRun) > 0 {
		parts, total := formatCounts(dryRun)
		fmt.Fprintf(&sb, "; dry-run (not applied): %s — total %d", parts, total)
	}
	if len(incomplete) > 0 {
		parts, total := formatCounts(incomplete)
		fmt.Fprintf(&sb, "; incomplete (never acted): %s — total %d", parts, total)
	}
	return sb.String()
}

// formatCounts renders "action n, action n" in alphabetical order plus the
// summed count.
func formatCounts(counts map[string]int) (string, int) {
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
	return strings.Join(parts, ", "), total
}

// SendDigest computes the 24h window ending at now, fetches action counts
// from src, builds the summary text, and sends it via sender. The caller
// decides how to treat a returned error (e.g. log-and-continue for
// best-effort delivery).
func SendDigest(ctx context.Context, sender AdminSender, adminChat int64, src DigestSource, now int64) error {
	since := now - 86400
	applied, dryRun, incomplete, err := src.ActionCountsSince(since)
	if err != nil {
		return err
	}
	text := BuildDigest(applied, dryRun, incomplete, "last 24h")
	_, err = sender.SendAdmin(ctx, adminChat, telegram.AdminMessage{Text: text})
	return err
}
