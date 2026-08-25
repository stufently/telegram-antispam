// Package incident runs the side-effecting state machine (spec §7). Ordering
// here is a Telegram API requirement: evidence is copied before any
// destructive action, because banning in a supergroup deletes prior messages.
package incident

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// errNothingCopied stands for a copyMessages call that reported success and
// copied nothing. It is not a Telegram error — there is no error to report —
// but the outcome it leaves behind is identical to a failed copy, so it is
// deliberately routed down the same branch instead of getting one of its own.
var errNothingCopied = errors.New("copyMessages copied nothing (message type not copyable?)")

// Repo is the persistence surface the machine needs; *store.DB satisfies it.
type Repo interface {
	InsertPending(chatID int64, messageID int, userID, senderChatID int64, dryRun bool, verdict domain.Verdict) (int64, bool, error)
	SetIncidentState(id int64, s domain.IncidentState) error
	AddEvidence(id int64, adminChatID int64, adminMessageIDs []int) error
	SaveIncidentTokens(id int64, tokens []string) error
	// SaveIncidentMessageIDs records every source message of the incident,
	// so a sanction applied later from the admin chat removes the whole
	// album rather than the one part the row is keyed on.
	SaveIncidentMessageIDs(id int64, ids []int) error
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
	buttonsFor func(incidentKey string, unenforced bool) [][]telegram.Button

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
func (m *Machine) SetButtons(fn func(incidentKey string, unenforced bool) [][]telegram.Button) {
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

	// An album's remaining ids, for a sanction applied later from the admin
	// chat: the row itself is keyed on one of them. Best-effort — failing to
	// record them must not stop the moderation that is about to happen; the
	// cost is a later manual enforce that removes only the keyed part.
	if len(inc.MessageIDs) > 1 {
		if err := m.repo.SaveIncidentMessageIDs(id, inc.MessageIDs); err != nil {
			log.Printf("incident %d: recording album message ids failed: %v", id, err)
		}
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
	// A successful copyMessages is not the same as evidence in the admin
	// chat. Telegram silently skips messages it cannot copy — a quiz poll is
	// the case seen in production, and a poll's option texts are part of what
	// the detectors read — and still reports no error, so the returned list can
	// come back short or empty. Empty is indistinguishable, from the admin
	// chat's side, from the copy having failed outright: a verdict card with
	// undo buttons and NOTHING under it to review. Fail it the same way, so
	// a probabilistic verdict is not enforced on evidence nobody can see.
	if copyErr == nil && len(adminIDs) == 0 {
		copyErr = errNothingCopied
	}

	// The chat's name for the card, fetched AFTER the copy: it is a
	// convenience, and evidence-first means nothing may queue ahead of the
	// copy. Best-effort AND time-boxed — the card is what tells the admins
	// anything happened, and the sanction waits behind it, so a getChat that
	// sits in a 429 retry must not hold either one. An error costs the card
	// its title; the chat id identifies the chat regardless.
	titleCtx, cancelTitle := context.WithTimeout(ctx, chatTitleBudget)
	chatTitle, titleErr := m.port.ChatTitle(titleCtx, inc.ChatID)
	cancelTitle()
	if titleErr != nil {
		log.Printf("incident %d: chat title lookup failed: %v", id, titleErr)
	}
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
			// Same header as a normal card: this is the ONLY trace left of
			// an incident whose evidence never arrived, so it is the card
			// that most needs to say which chat and who.
			// acting says whether a sanction actually follows: without the
			// evidence, a probabilistic verdict is dropped, and the card
			// must not name an action nobody will apply.
			Text: formatCard(id, inc, chatTitle, fmt.Sprintf("evidence copy failed: %v; %s", copyErr, tail), acting && !inc.DryRun),
		}
		if m.buttonsFor != nil {
			// No enforce button on this card, even in dry-run: the evidence
			// copy is what failed, so the moderator is being asked to act on
			// a message they cannot see. Undo and evidence buttons stay.
			msg.Buttons = m.buttonsFor(key, false)
		}
		_, sendErr := m.port.SendAdmin(ctx, m.adminChatID, msg)
		if !acting {
			if sendErr != nil {
				err := fmt.Errorf("evidence copy failed (%v), admins not notified either: %w", copyErr, sendErr)
				m.logOutcome(id, inc, "not enforced", "stage=admin_notify", err)
				return err
			}
			err := fmt.Errorf("evidence copy failed, not acting on a probabilistic verdict: %w", copyErr)
			m.logOutcome(id, inc, "not enforced", "stage=evidence_copy", err)
			return err
		}
		if sendErr != nil {
			err := fmt.Errorf("send admin: %w", sendErr)
			m.logOutcome(id, inc, "not enforced", "stage=admin_notify", err)
			return err
		}
	} else {
		key := fmt.Sprintf("%d", id)
		// Some evidence arrived, but maybe not all of it: an album whose
		// poll or unsupported part Telegram declined to copy lands here with
		// a short list and no error. The sanction still applies — dropping it
		// would let one uncopyable part shield the whole album — but the card
		// has to SAY so, because the part that actually triggered the verdict
		// may be one of the missing ones: an album is judged on the single
		// part carrying its text, and copyMessages returns destination ids
		// with no mapping back, so we know how many parts are missing and
		// never which. Without the line a moderator reviewing a false
		// positive reads the copies as the whole message and overturns, or
		// upholds, a verdict on a picture they have only part of.
		note := ""
		if got, want := len(adminIDs), len(inc.MessageIDs); got < want {
			note = fmt.Sprintf("evidence INCOMPLETE: copied %d of %d messages — the part that triggered the verdict may be missing", got, want)
		}
		msg := telegram.AdminMessage{
			IncidentKey:      key,
			SourceChatID:     inc.ChatID,
			CopiedFromChatID: inc.ChatID,
			CopyMessageIDs:   adminIDs,
			Text:             formatCard(id, inc, chatTitle, note, !inc.DryRun),
		}
		if m.buttonsFor != nil {
			// A dry-run incident (a review verdict, or a chat still in
			// observation) is one nothing was done about yet, so its card
			// gets the extra button that does it. See admin.Buttons.
			msg.Buttons = m.buttonsFor(key, inc.DryRun)
		}
		if _, err := m.port.SendAdmin(ctx, m.adminChatID, msg); err != nil {
			err = fmt.Errorf("send admin: %w", err)
			m.logOutcome(id, inc, "not enforced", "stage=admin_notify", err)
			return err
		}
		if err := m.repo.AddEvidence(id, m.adminChatID, adminIDs); err != nil {
			err = fmt.Errorf("save evidence: %w", err)
			m.logOutcome(id, inc, "not enforced", "stage=evidence_store", err)
			return err
		}
		m.setState(id, domain.StateEvidenced)
	}

	// A review verdict is never enforced automatically, whatever the chat's
	// mode says. The wiring already turns such an incident into a dry-run
	// one; this second check is deliberate duplication, because the cost of
	// the two disagreeing is a real mute nobody asked for.
	if inc.DryRun || inc.Verdict.ReviewOnly {
		m.logOutcome(id, inc, "not enforced", fmt.Sprintf("dry_run=%t review_only=%t", inc.DryRun, inc.Verdict.ReviewOnly), nil)
		return m.repo.SetIncidentState(id, domain.StateDone)
	}

	// 2-3. sanction, then delete the originals.
	out := m.Enforce(ctx, id, inc)
	if out.Err != nil {
		return out.Err
	}

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

// Outcome reports what an Enforce attempt actually managed to do. The two
// halves are reported separately because they fail independently, and the
// caller's next move differs: a sanction that landed must not be applied a
// second time by a retry, while a delete that failed is worth retrying (or
// telling the moderator to finish by hand). A single error return could not
// express "muted, but the message is still there".
type Outcome struct {
	Sanctioned bool  // the ban/mute/restrict call succeeded (or was a no-op)
	Deleted    bool  // the original messages were removed
	Err        error // what went wrong, if anything
}

// Enforce applies the sanction and removes the offending message, recording
// how far it got. It is the second half of Handle, exported because it is
// also the whole job of the admin-chat "enforce" button: an incident raised
// for review (or in a dry-run chat) has evidence and buttons but was never
// acted on, and the moderator who decides it IS spam needs the same two
// effects to happen now, from the same code path — a second implementation
// would be a second set of ordering bugs.
//
// The caller is responsible for deciding that enforcement is wanted at all;
// Enforce does not consult DryRun.
func (m *Machine) Enforce(ctx context.Context, id int64, inc domain.Incident) Outcome {
	var out Outcome
	actErr := m.applyAction(ctx, inc)
	if actErr == nil {
		out.Sanctioned = true
		m.setState(id, domain.StateActed)
	}

	// Delete the originals last — ALSO when the sanction failed. Returning
	// early on a failed sanction (as this did) left the spam standing in the
	// chat: the one half of moderation that always works was skipped because
	// the other half errored. Deleting is independent of banning, so it runs
	// either way and the sanction error is reported afterwards.
	delErr := m.port.DeleteMessages(ctx, inc.ChatID, inc.MessageIDs)
	if delErr == nil {
		out.Deleted = true
	}

	switch {
	case actErr != nil && delErr != nil:
		out.Err = fmt.Errorf("apply action: %w (originals also not deleted: %v)", actErr, delErr)
	case delErr != nil:
		out.Err = fmt.Errorf("delete originals: %w", delErr)
	case actErr != nil:
		out.Err = fmt.Errorf("apply action: %w (originals deleted)", actErr)
	default:
		m.setState(id, domain.StateCleaned)
	}
	m.logOutcome(id, inc, "enforced", fmt.Sprintf("outcome=%s action_ok=%t deleted=%t",
		outcomeOf(out), out.Sanctioned, out.Deleted), out.Err)
	return out
}

// logOutcome records what was done about an incident, as the counterpart of
// the "observed" line package telegram writes for a message that passed.
//
// Without it the log answers "why did this pass?" and nothing else: every
// applied sanction was invisible there, recoverable only from SQLite or the
// metrics, so neither a grep nor a log-based alert could see the bot act.
// Enforce is the single place all three sanction paths — the automatic one,
// the admin chat's enforce button and the /spam, /ban commands — pass
// through, so one line here covers them all; the branches that end an
// incident before it gets that far say so with a stage= instead.
//
// action_ok, not "sanctioned": applyAction succeeds trivially for
// delete_only and quarantine, where no sanction is applied at all, and a
// field that reads true for those would misreport what happened.
func (m *Machine) logOutcome(id int64, inc domain.Incident, what, detail string, err error) {
	msgID := 0
	if len(inc.MessageIDs) > 0 {
		msgID = inc.MessageIDs[0]
	}
	parts := ""
	if len(inc.MessageIDs) > 1 {
		parts = fmt.Sprintf(" parts=%d", len(inc.MessageIDs))
	}
	tail := ""
	if err != nil {
		tail = ": " + sanitize(err.Error())
	}
	log.Printf("chat=%d msg=%d sender=%s: %s incident=%d action=%s %s%s [%s]%s",
		inc.ChatID, msgID, inc.Sender.Kind, what, id, inc.Verdict.Action,
		detail, parts, signalNames(inc.Verdict.Signals), tail)
}

// outcomeOf names what enforcement actually achieved, because the two halves
// fail independently and "enforced" alone would be a lie for the case where
// neither landed: an alert counting "enforced" lines would report sanctions
// that never happened.
func outcomeOf(out Outcome) string {
	switch {
	case out.Sanctioned && out.Deleted:
		return "succeeded"
	case out.Sanctioned || out.Deleted:
		return "partial"
	default:
		return "failed"
	}
}

// signalNames lists which detectors fired, and deliberately NOT what they
// saw. Signal.Detail is fine on the pass path, but this line is written for
// messages the bot acted on, where the detail can be a person's @tag or
// display name (fake_admin) or a configured phrase that matched the whole
// message (deny_exact). The full detail stays where it is already kept: the
// admin card and the audit table.
func signalNames(sigs []domain.Signal) string {
	if len(sigs) == 0 {
		return "no signals"
	}
	names := make([]string, 0, len(sigs))
	for _, s := range sigs {
		names = append(names, sanitize(s.Name))
	}
	return strings.Join(names, " ")
}

// sanitize keeps one incident to one log line. Signal names are literals in
// this codebase and Telegram's error strings are not user-authored, so
// neither is a live injection vector today — but both are free-form strings
// reaching a line that monitoring parses, and a newline in one of them would
// let a second, forged record appear. Cheaper to fold them here than to rely
// on that staying true.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || r < 0x20,
			r >= 0x7f && r <= 0x9f,     // DEL and the C1 controls, incl. NEL
			r == 0x2028 || r == 0x2029: // line and paragraph separators
			return ' '
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x061c:
			// Bidi overrides. A spammer's display name carrying a
			// right-to-left override reverses everything printed AFTER it,
			// so a line can be made to read as something it is not — in a
			// log grep and on the admin card alike.
			return -1
		case r >= 0x200b && r <= 0x200f, r == 0xfeff:
			// Zero-width space / joiners / marks: invisible, and enough of
			// them turn a name into a wall of nothing.
			return -1
		}
		return r
	}, s)
}

// chatTitleBudget bounds the optional chat-title lookup. It is short on
// purpose: everything the incident still has to do — telling the admins,
// applying the sanction — queues behind it, and a title is worth none of that.
const chatTitleBudget = 3 * time.Second

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
