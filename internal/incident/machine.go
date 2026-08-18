// Package incident runs the side-effecting state machine (spec §7). Ordering
// here is a Telegram API requirement: evidence is copied before any
// destructive action, because banning in a supergroup deletes prior messages.
package incident

import (
	"context"
	"fmt"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// Repo is the persistence surface the machine needs; *store.DB satisfies it.
type Repo interface {
	InsertPending(chatID int64, messageID int, userID int64, dryRun bool) (int64, bool, error)
	SetIncidentState(id int64, s domain.IncidentState) error
	AddEvidence(id int64, adminChatID int64, adminMessageIDs []int) error
}

// hardConfidence is the floor above which we still act even if evidence copy
// fails (a hard deny keeps metadata and blocks); below it we stop.
const hardConfidence = 0.9

type Machine struct {
	port        telegram.Port
	repo        Repo
	adminChatID int64

	// buttonsFor renders the admin action buttons for an incident key. It is
	// a func rather than a direct import of package admin because package
	// admin (Handler.dispatch) needs telegram.Port too, and importing it
	// here would create an import cycle; main wires the real implementation
	// via SetButtons.
	buttonsFor func(incidentKey string) [][]telegram.Button
}

func New(port telegram.Port, repo Repo, adminChatID int64) *Machine {
	return &Machine{port: port, repo: repo, adminChatID: adminChatID}
}

// SetButtons installs a provider that renders the admin action buttons for an
// incident key. If unset, the admin message has no buttons.
func (m *Machine) SetButtons(fn func(incidentKey string) [][]telegram.Button) {
	m.buttonsFor = fn
}

func (m *Machine) Handle(ctx context.Context, inc domain.Incident) error {
	if len(inc.MessageIDs) == 0 {
		return fmt.Errorf("incident has no message ids")
	}
	id, fresh, err := m.repo.InsertPending(inc.ChatID, inc.MessageIDs[0], inc.Sender.UserID, inc.DryRun)
	if err != nil {
		return fmt.Errorf("insert pending: %w", err)
	}
	if !fresh {
		// reprocess guard: this incident was already recorded, so evidence
		// was already copied and any action already taken. Skip entirely.
		return nil
	}

	// 1. evidence BEFORE any destructive action.
	adminIDs, copyErr := m.port.CopyMessages(ctx, m.adminChatID, inc.ChatID, inc.MessageIDs)
	if copyErr != nil {
		_ = m.repo.SetIncidentState(id, domain.StateEvidenceFailed)
		if inc.Verdict.Confidence < hardConfidence {
			return fmt.Errorf("evidence copy failed, not acting on low confidence: %w", copyErr)
		}
		// hard deny: proceed without copied evidence but notify admins so
		// they know an action was taken without a copied evidence trail.
		key := fmt.Sprintf("%d", id)
		msg := telegram.AdminMessage{
			IncidentKey:      key,
			SourceChatID:     inc.ChatID,
			CopiedFromChatID: inc.ChatID,
			CopyMessageIDs:   nil,
			Text:             fmt.Sprintf("evidence copy failed: %v; %s", copyErr, inc.Verdict.Reason),
		}
		if m.buttonsFor != nil {
			msg.Buttons = m.buttonsFor(key)
		}
		if _, err := m.port.SendAdmin(ctx, m.adminChatID, msg); err != nil {
			return fmt.Errorf("send admin: %w", err)
		}
	} else {
		key := fmt.Sprintf("%d", id)
		msg := telegram.AdminMessage{
			IncidentKey:      key,
			SourceChatID:     inc.ChatID,
			CopiedFromChatID: inc.ChatID,
			CopyMessageIDs:   adminIDs,
			Text:             inc.Verdict.Reason,
		}
		if m.buttonsFor != nil {
			msg.Buttons = m.buttonsFor(key)
		}
		if _, err := m.port.SendAdmin(ctx, m.adminChatID, msg); err != nil {
			return fmt.Errorf("send admin: %w", err)
		}
		if err := m.repo.AddEvidence(id, m.adminChatID, adminIDs); err != nil {
			return fmt.Errorf("save evidence: %w", err)
		}
		_ = m.repo.SetIncidentState(id, domain.StateEvidenced)
	}

	if inc.DryRun {
		return m.repo.SetIncidentState(id, domain.StateDone)
	}

	// 2. apply action.
	if err := m.applyAction(ctx, inc); err != nil {
		return fmt.Errorf("apply action: %w", err)
	}
	_ = m.repo.SetIncidentState(id, domain.StateActed)

	// 3. delete originals last.
	if err := m.port.DeleteMessages(ctx, inc.ChatID, inc.MessageIDs); err != nil {
		return fmt.Errorf("delete originals: %w", err)
	}
	_ = m.repo.SetIncidentState(id, domain.StateCleaned)

	return m.repo.SetIncidentState(id, domain.StateDone)
}

func (m *Machine) applyAction(ctx context.Context, inc domain.Incident) error {
	switch inc.Verdict.Action {
	case domain.ActionBan:
		return m.port.BanMember(ctx, inc.ChatID, inc.Sender.UserID)
	case domain.ActionMute, domain.ActionDeleteMute:
		return m.port.RestrictMember(ctx, inc.ChatID, inc.Sender.UserID, telegram.Perms{CanSend: false}, 0)
	case domain.ActionDeleteOnly, domain.ActionQuarantine, domain.ActionNone:
		return nil
	default:
		return fmt.Errorf("unknown action %q", inc.Verdict.Action)
	}
}
