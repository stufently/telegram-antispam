// Package watch holds update-driven, non-message side effects: small
// watchers that react to Telegram update types other than plain messages
// (e.g. chat_member updates) and record state or raise admin notices.
package watch

import (
	"context"
	"fmt"
	"strings"

	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// IdentityStore is the store dependency MemberWatcher needs. *store.DB
// satisfies it.
type IdentityStore interface {
	UpsertIdentity(chatID, userID int64, username, displayName string) (string, string, bool, error)
}

// MemberEvent is the narrow, library-free shape the wiring extracts from a
// ChatMemberUpdated update.
type MemberEvent struct {
	ChatID, UserID        int64
	Username, DisplayName string
}

// MemberWatcher tracks each user's last-known name and raises an admin-chat
// notice when someone renames into an admin-like name after joining.
type MemberWatcher struct {
	Store       IdentityStore
	Admins      detect.AdminSource
	AdminChatID int64
	Port        telegram.Port
	MaxDistance int
	Enabled     bool
}

// Observe records the incoming identity and, if enabled and the name
// actually changed, checks whether the new name looks like an admin's and
// raises a best-effort admin notice on the first match.
func (w *MemberWatcher) Observe(ctx context.Context, e MemberEvent) error {
	prevU, prevD, changed, err := w.Store.UpsertIdentity(e.ChatID, e.UserID, e.Username, e.DisplayName)
	if err != nil {
		return err
	}

	if !w.Enabled || !changed {
		return nil
	}

	if w.Admins == nil {
		return nil
	}

	newUsername := strings.ToLower(e.Username)
	newDisplay := strings.ToLower(e.DisplayName)

	admins, err := w.Admins.AdminIdentities(e.ChatID)
	if err != nil {
		return fmt.Errorf("get chat administrators: %w", err)
	}

	for _, admin := range admins {
		if admin.UserID != 0 && admin.UserID == e.UserID {
			// An admin renaming themselves matches their own admin-list
			// entry by construction; that's not impersonation.
			continue
		}
		label, matched := matchAdmin(newUsername, newDisplay, admin, w.MaxDistance)
		if !matched {
			continue
		}

		msg := telegram.AdminMessage{
			Text: fmt.Sprintf(
				"possible admin impersonation in chat %d: user %d renamed to %q/%q (was %q/%q), matches admin %q",
				e.ChatID, e.UserID, e.Username, e.DisplayName, prevU, prevD, label,
			),
			SourceChatID: e.ChatID,
		}
		_, _ = w.Port.SendAdmin(ctx, w.AdminChatID, msg)
		return nil
	}

	return nil
}

// matchAdmin reports whether the (casefolded) new username or display name
// is within maxDistance of the admin's username, display name, or custom
// title, skipping empty fields on either side so an empty string never
// matches. It returns the matched admin field's original-case value as a
// label for the notice.
func matchAdmin(newUsername, newDisplay string, admin detect.AdminIdentity, maxDistance int) (string, bool) {
	fields := []string{admin.Username, admin.DisplayName, admin.CustomTitle}
	for _, field := range fields {
		if field == "" {
			continue
		}
		lowerField := strings.ToLower(field)
		if newUsername != "" && detect.LevenshteinWithin(newUsername, lowerField, maxDistance) {
			return field, true
		}
		if newDisplay != "" && detect.LevenshteinWithin(newDisplay, lowerField, maxDistance) {
			return field, true
		}
	}
	return "", false
}
