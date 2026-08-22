// Package incident runs the side-effecting state machine (spec §7). Ordering
// here is a Telegram API requirement: evidence is copied before any
// destructive action, because banning in a supergroup deletes prior messages.
package incident

import (
	"context"
	"fmt"
	"log"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// Repo is the persistence surface the machine needs; *store.DB satisfies it.
type Repo interface {
	InsertPending(chatID int64, messageID int, userID, senderChatID int64, dryRun bool, verdict domain.Verdict) (int64, bool, error)
	SetIncidentState(id int64, s domain.IncidentState) error
	AddEvidence(id int64, adminChatID int64, adminMessageIDs []int) error
	SaveIncidentTokens(id int64, tokens []string) error
}

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

	// EphemeralNotice enables a best-effort, per-user-visible notice sent
	// after a live sanction, telling the user their message was removed
	// (spec §12: ephemeral is never the sole verification path). Off by
	// default; set directly by the wiring that constructs the Machine.
	EphemeralNotice bool
	// EphemeralText is the notice text sent when EphemeralNotice is true.
	EphemeralText string
}

func New(port telegram.Port, repo Repo, adminChatID int64) *Machine {
	return &Machine{port: port, repo: repo, adminChatID: adminChatID}
}

// SetButtons installs a provider that renders the admin action buttons for an
// incident key. If unset, the admin message has no buttons.
func (m *Machine) SetButtons(fn func(incidentKey string) [][]telegram.Button) {
	m.buttonsFor = fn
}

// Handle drives one incident to completion. It hides the freshness flag: a
// duplicate is not an error for the automatic path, which fires per message
// and must stay silent when it re-sees one.
func (m *Machine) Handle(ctx context.Context, inc domain.Incident) error {
	_, err := m.HandleReport(ctx, inc)
	return err
}

// HandleReport is Handle for callers that must distinguish "acted" from
// "this message was already handled". A moderator typing /spam deserves that
// distinction: silently doing nothing would read as a broken command, and
// the corpus training that follows a manual report has to run either way.
func (m *Machine) HandleReport(ctx context.Context, inc domain.Incident) (fresh bool, err error) {
	if err := m.handle(ctx, inc, &fresh); err != nil {
		return fresh, err
	}
	return fresh, nil
}

func (m *Machine) handle(ctx context.Context, inc domain.Incident, freshOut *bool) error {
	if len(inc.MessageIDs) == 0 {
		return fmt.Errorf("incident has no message ids")
	}
	id, fresh, err := m.repo.InsertPending(inc.ChatID, inc.MessageIDs[0], inc.Sender.UserID, inc.Sender.SenderChatID, inc.DryRun, inc.Verdict)
	if err != nil {
		return fmt.Errorf("insert pending: %w", err)
	}
	if freshOut != nil {
		*freshOut = fresh
	}
	if !fresh {
		// reprocess guard: this incident was already recorded, so evidence
		// was already copied and any action already taken. Skip entirely.
		return nil
	}

	// Capture the normalized tokens before anything destructive: they are
	// what an admin's later Confirm-spam / False-positive press trains on,
	// and after the originals are deleted there is no way to recover them.
	// Best-effort — a failure here must not block moderation.
	if len(inc.Tokens) > 0 {
		if err := m.repo.SaveIncidentTokens(id, inc.Tokens); err != nil {
			log.Printf("save incident %d tokens: %v", id, err)
		}
	}

	// 1. evidence BEFORE any destructive action.
	adminIDs, copyErr := m.port.CopyMessages(ctx, m.adminChatID, inc.ChatID, inc.MessageIDs)
	if copyErr != nil {
		m.setState(id, domain.StateEvidenceFailed)
		acting := actsWithoutEvidence(inc.Verdict)
		// Tell the admins either way, and BEFORE deciding whether to act:
		// "detected but not acted on" is exactly the outcome that must not
		// be silent, and the notification is the only trace left once the
		// evidence copy is gone.
		key := fmt.Sprintf("%d", id)
		tail := "not acting without evidence — review manually"
		if acting {
			tail = "acting without a copied evidence trail"
		}
		msg := telegram.AdminMessage{
			IncidentKey:      key,
			SourceChatID:     inc.ChatID,
			CopiedFromChatID: inc.ChatID,
			CopyMessageIDs:   nil,
			Text:             fmt.Sprintf("evidence copy failed: %v; %s; %s", copyErr, inc.Verdict.Reason, tail),
		}
		if m.buttonsFor != nil {
			msg.Buttons = m.buttonsFor(key)
		}
		_, sendErr := m.port.SendAdmin(ctx, m.adminChatID, msg)
		if !acting {
			if sendErr != nil {
				return fmt.Errorf("evidence copy failed (%v), admins not notified either: %w", copyErr, sendErr)
			}
			return fmt.Errorf("evidence copy failed, not acting on a probabilistic verdict: %w", copyErr)
		}
		if sendErr != nil {
			return fmt.Errorf("send admin: %w", sendErr)
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
		m.setState(id, domain.StateEvidenced)
	}

	if inc.DryRun {
		return m.repo.SetIncidentState(id, domain.StateDone)
	}

	// 2. apply action.
	actErr := m.applyAction(ctx, inc)
	if actErr == nil {
		m.setState(id, domain.StateActed)
	}

	// 3. delete originals last — ALSO when the sanction failed. Returning
	// early on a failed sanction (as this did) left the spam standing in the
	// chat: the one half of moderation that always works was skipped because
	// the other half errored. Deleting is independent of banning, so it runs
	// either way and the sanction error is reported afterwards.
	if err := m.port.DeleteMessages(ctx, inc.ChatID, inc.MessageIDs); err != nil {
		if actErr != nil {
			return fmt.Errorf("apply action: %w (originals also not deleted: %v)", actErr, err)
		}
		return fmt.Errorf("delete originals: %w", err)
	}
	if actErr != nil {
		return fmt.Errorf("apply action: %w (originals deleted)", actErr)
	}
	m.setState(id, domain.StateCleaned)

	if m.EphemeralNotice && m.EphemeralText != "" && inc.Sender.UserID != 0 {
		// Best-effort, per-user-visible notice. Delivery is not guaranteed
		// (spec §12) and a failure must never fail the incident, so the
		// error is intentionally ignored.
		_, _ = m.port.SendEphemeral(ctx, inc.ChatID, inc.Sender.UserID, m.EphemeralText)
	}

	return m.repo.SetIncidentState(id, domain.StateDone)
}

// actsWithoutEvidence reports whether a verdict may be enforced when the
// evidence copy into the admin chat failed.
//
// The gate used to be Confidence >= 0.9, which never actually
// gated anything: every detector, and the LLM stage too, stamps exactly
// 1.0, so the branch meant "always act". That is the wrong default here —
// the sanction is reversible only through the buttons under the evidence
// message, so acting with no evidence produces a mute nobody can review.
//
// Confidence is not the right axis either. What matters is whether the
// verdict rests on OUR judgement or on an external fact: a CAS/LOLS
// blocklist hit is a globally published ban that an admin can verify
// without the copied message, while bayes / llm / behavior are exactly the
// calls a human is supposed to double-check. So only the blocklist acts
// blind; everything else fails closed and merely reports.
func actsWithoutEvidence(v domain.Verdict) bool {
	for _, s := range v.Signals {
		if s.Name == "blocklist" {
			return true
		}
	}
	return false
}

// setState records how far the incident got, logging a write failure rather
// than discarding it.
//
// The error is not returned: the sanction has already happened in Telegram
// and unwinding it here would be worse than a stale row. But it must not be
// invisible either — the undo buttons refuse to act on an incident whose
// state never reached "acted" (see store.IncidentRow.Sanctioned), so a lost
// write means a real, applied sanction that the admin chat believes never
// happened. The log line is what turns that into something diagnosable.
func (m *Machine) setState(id int64, st domain.IncidentState) {
	if err := m.repo.SetIncidentState(id, st); err != nil {
		log.Printf("incident %d: recording state %s failed: %v", id, st, err)
	}
}

func (m *Machine) applyAction(ctx context.Context, inc domain.Incident) error {
	// A message sent on behalf of a channel has no member to sanction: its
	// author is the channel, and Telegram exposes banChatSenderChat for
	// exactly this case. banChatMember/restrictChatMember against the
	// pseudo-user behind such a message (id 136817688, or 0 when the API
	// sends no `from` at all) fails with 400 — which used to abort the
	// incident before the message was even deleted. Muting is meaningless
	// for a channel, so both punitive actions map onto the same call.
	if inc.Sender.Kind == domain.SenderExternalChannel && inc.Sender.SenderChatID != 0 {
		switch inc.Verdict.Action {
		case domain.ActionBan, domain.ActionMute, domain.ActionDeleteMute:
			return m.port.BanSenderChat(ctx, inc.ChatID, inc.Sender.SenderChatID)
		}
	}
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
