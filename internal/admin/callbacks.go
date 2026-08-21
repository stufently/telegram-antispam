// Package admin handles admin-chat inline button presses (the evidence
// message's action row). Every action here is destructive or
// learning-affecting, so RBAC — Authorized — gates every branch of Handle:
// no store or Telegram side effect may run before it returns true.
package admin

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// Action identifies which admin-chat button was pressed.
type Action string

const (
	ActFalsePositive  Action = "fp"
	ActLiftNoLearn    Action = "lift"
	ActConfirmSpam    Action = "confirm"
	ActDeleteEvidence Action = "delevi"
)

// Callback is a normalized incoming callback query.
type Callback struct {
	ID        string // Telegram callback query id, for AnswerCallback.
	Data      string // "<act>:<incidentKey>".
	PresserID int64  // Telegram user id of whoever pressed the button.
	// AdminChatID and MessageID locate the admin-chat message the button
	// sits on. They are informational for most actions; "Delete evidence"
	// needs the chat id to remove the copies the bot posted there.
	AdminChatID int64
	MessageID   int
}

// Buttons lays out the 4 admin actions for one incident. Each button's Data
// is "<act>:<incidentKey>", which must stay within Telegram's 64-byte
// callback_data limit; incidentKey is expected to be a short opaque id (the
// incident's decimal row id in M2), leaving ample headroom.
func Buttons(incidentKey string) [][]telegram.Button {
	return [][]telegram.Button{
		{
			{Text: "False positive", Data: encode(ActFalsePositive, incidentKey)},
			{Text: "Lift (no learn)", Data: encode(ActLiftNoLearn, incidentKey)},
		},
		{
			{Text: "Confirm spam", Data: encode(ActConfirmSpam, incidentKey)},
			{Text: "Delete evidence", Data: encode(ActDeleteEvidence, incidentKey)},
		},
	}
}

func encode(act Action, incidentKey string) string {
	return string(act) + ":" + incidentKey
}

// ParseCallback splits callback_data of the form "act:key" produced by
// Buttons. It rejects anything that isn't a recognized Action so Handle
// never dispatches on unknown input.
func ParseCallback(data string) (Action, string, bool) {
	act, key, found := strings.Cut(data, ":")
	if !found || act == "" || key == "" {
		return "", "", false
	}
	switch Action(act) {
	case ActFalsePositive, ActLiftNoLearn, ActConfirmSpam, ActDeleteEvidence:
		return Action(act), key, true
	default:
		return "", "", false
	}
}

// Handler dispatches admin-chat callback queries, gated by per-callback
// RBAC: the presser must be a global operator or an admin of the incident's
// source chat.
type Handler struct {
	port      telegram.Port
	db        *store.DB
	operators map[int64]bool
	trainer   func(scope, label string, tokens []string) error
}

// NewHandler builds a Handler. operators is the set of Telegram user ids
// treated as global operators regardless of chat; a nil map means no global
// operators (source-chat admins can still act).
func NewHandler(port telegram.Port, db *store.DB, operators map[int64]bool) *Handler {
	if operators == nil {
		operators = map[int64]bool{}
	}
	return &Handler{port: port, db: db, operators: operators}
}

// SetTrainer installs a best-effort Bayes trainer invoked on confirm-spam /
// false-positive. It takes tokens rather than text because that is all the
// incident kept (see store.SaveIncidentTokens): the raw message is deleted
// from the source chat before an admin ever sees the button. A nil trainer
// (the default) disables training.
func (h *Handler) SetTrainer(t func(scope, label string, tokens []string) error) { h.trainer = t }

// Authorized reports whether presserID may act on incidents whose source
// chat is sourceChatID. It checks the cheap, network-free global-operator
// set first, then falls back to a live chat-admin membership check.
func (h *Handler) Authorized(ctx context.Context, sourceChatID, presserID int64) (bool, error) {
	if h.operators[presserID] {
		return true, nil
	}
	admins, err := h.port.GetChatAdministrators(ctx, sourceChatID)
	if err != nil {
		return false, fmt.Errorf("get chat administrators: %w", err)
	}
	for _, m := range admins {
		if m.UserID == presserID {
			return true, nil
		}
	}
	return false, nil
}

// Handle processes one admin-chat callback query: parse, resolve the
// incident's source chat, RBAC-check, dispatch, answer. No action branch
// below the Authorized call may run when it returns false.
func (h *Handler) Handle(ctx context.Context, cb Callback) error {
	if h.db == nil {
		// Defensive: the real wiring always passes a live db. Answer rather
		// than dispatch against a nil store, which would panic.
		return h.port.AnswerCallback(ctx, cb.ID, "admin store unavailable")
	}

	act, key, ok := ParseCallback(cb.Data)
	if !ok {
		return h.port.AnswerCallback(ctx, cb.ID, "invalid callback")
	}

	incidentID, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return h.port.AnswerCallback(ctx, cb.ID, "invalid incident")
	}

	inc, err := h.db.GetIncident(incidentID)
	if err != nil {
		return fmt.Errorf("lookup incident %d: %w", incidentID, err)
	}

	authorized, err := h.Authorized(ctx, inc.ChatID, cb.PresserID)
	if err != nil {
		return fmt.Errorf("authorize presser %d: %w", cb.PresserID, err)
	}
	if !authorized {
		// Reject: no action branch below this point runs for an
		// unauthorized presser.
		return h.port.AnswerCallback(ctx, cb.ID, "not authorized")
	}

	text, err := h.dispatch(ctx, act, inc, cb)
	if err != nil {
		return fmt.Errorf("dispatch %s: %w", act, err)
	}
	return h.port.AnswerCallback(ctx, cb.ID, text)
}

// dispatch applies the per-action effect. Every call here is only reached
// once Handle has confirmed Authorized == true.
//
// Two properties are deliberate. First, the undo actions really call
// Telegram (unban / full unmute) instead of only recording a row: a live
// sanction that no button can lift is worse than no button at all. Second,
// what undo CANNOT do is restore deleted messages — Telegram offers no such
// call — so the reply text says so rather than implying a full rollback.
func (h *Handler) dispatch(ctx context.Context, act Action, inc store.IncidentRow, cb Callback) (string, error) {
	key := strconv.FormatInt(inc.ID, 10)

	// One decision per incident. The buttons stay in the admin chat forever,
	// so without this claim a press on an old evidence message would issue a
	// fresh unban today — possibly lifting a LATER, unrelated sanction on the
	// same user, which Telegram gives us no way to distinguish. It also stops
	// an incident being marked both spam and false-positive.
	if act != ActDeleteEvidence {
		claimed, existing, err := h.db.RecordDecision(inc.ID, string(act))
		if err != nil {
			return "", err
		}
		if !claimed {
			return "already decided: " + decisionLabel(existing), nil
		}
	}

	switch act {
	case ActFalsePositive:
		// Order matters, and it is the reverse of what it used to be. The
		// audit sample used to be written first, "so it survives a failed
		// undo" — but a failed undo now releases the claim so the button
		// can be pressed again, and a surviving `fp` sample would then sit
		// in the audit next to whatever the retry decided (possibly
		// `confirm`), describing a decision that never took effect. The
		// sanction is lifted first; only then is the decision recorded.
		lifted, err := h.undo(ctx, inc)
		if err != nil {
			h.releaseClaim(inc.ID, act)
			return "", err
		}
		if _, err := h.db.InsertSample(decisionScope, string(act), "user", key); err != nil {
			// The sanction IS lifted at this point, so the claim stands:
			// re-pressing would only issue a second, pointless unban.
			return "", err
		}
		trained := h.train(inc.ID, "ham")
		return joinReply("marked false positive", lifted, trained), nil

	case ActConfirmSpam:
		if _, err := h.db.InsertSample(decisionScope, string(act), "user", key); err != nil {
			// Nothing happened in Telegram here — confirming spam only
			// records and trains — so the claim must go back, or a failed
			// write would answer "already decided" forever.
			h.releaseClaim(inc.ID, act)
			return "", err
		}
		trained := h.train(inc.ID, "spam")
		return joinReply("confirmed spam", "", trained), nil

	case ActLiftNoLearn:
		// Explicitly not a learning signal: no sample write, and the
		// captured tokens are dropped rather than fed to Bayes.
		lifted, err := h.undo(ctx, inc)
		if err != nil {
			h.releaseClaim(inc.ID, act)
			return "", err
		}
		h.dropTokens(inc.ID)
		return joinReply("lifted (not learned)", lifted, ""), nil

	case ActDeleteEvidence:
		n, err := h.deleteEvidence(ctx, inc.ID)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "no evidence to delete", nil
		}
		return fmt.Sprintf("deleted %d evidence message(s)", n), nil

	default:
		return "unknown action", nil
	}
}

// releaseClaim hands back the one-decision claim after the Telegram call
// that was supposed to act on it failed, so the moderator can press the
// button again. Without it a transient API error was permanent: the
// incident counted as decided and the sanction it was meant to lift stayed
// in place. Failing to release is logged, not returned — the caller is
// already reporting the original, more useful error.
func (h *Handler) releaseClaim(incidentID int64, act Action) {
	if err := h.db.ReleaseDecision(incidentID, string(act)); err != nil {
		log.Printf("release decision claim on incident %d: %v", incidentID, err)
	}
}

// decisionLabel renders a stored decision for the callback toast.
func decisionLabel(decision string) string {
	switch Action(decision) {
	case ActFalsePositive:
		return "false positive"
	case ActConfirmSpam:
		return "confirmed spam"
	case ActLiftNoLearn:
		return "lifted"
	case "":
		// Only reachable if the row vanished between the claim and the read.
		return "unknown"
	default:
		return decision
	}
}

// decisionScope is the samples-table scope used for admin decision records.
// It is intentionally NOT a Bayes scope: nothing reads counts under it, so
// these rows are an audit of who decided what, never training data. Bayes
// learning goes through train (below) under the real corpus scope.
const decisionScope = "incident"

// undo lifts the sanction this incident applied, returning a short phrase
// describing what was reverted (empty when there was nothing to revert).
//
// A dry-run incident, an incident that never reached StateActed, or a
// delete-only action all yield "" without touching Telegram: issuing an
// unban for a sanction that was never applied would be a lie in the audit
// trail and a wasted API call under the queue's rate limit.
func (h *Handler) undo(ctx context.Context, inc store.IncidentRow) (string, error) {
	if !inc.Sanctioned() {
		return "", nil
	}
	// A channel that posted into the group was sanctioned with
	// banChatSenderChat, so it is lifted with the matching call and keyed on
	// the CHANNEL id. Checked before the user branch: for such an incident
	// UserID is 0 or a Telegram pseudo-user, which would make the ordinary
	// unban either a no-op or an action against the wrong account.
	if inc.SenderChatID != 0 {
		if err := h.port.UnbanSenderChat(ctx, inc.ChatID, inc.SenderChatID); err != nil {
			return "", fmt.Errorf("unban channel %d in chat %d: %w", inc.SenderChatID, inc.ChatID, err)
		}
		return "channel unbanned", nil
	}
	if inc.UserID == 0 {
		return "", nil
	}
	switch inc.Action {
	case domain.ActionBan:
		if err := h.port.UnbanMember(ctx, inc.ChatID, inc.UserID); err != nil {
			return "", fmt.Errorf("unban user %d in chat %d: %w", inc.UserID, inc.ChatID, err)
		}
		return "unbanned", nil
	case domain.ActionMute, domain.ActionDeleteMute:
		// UnrestrictMember, not RestrictMember with a permissive Perms:
		// restrictChatMember replaces the whole permission set, so anything
		// Perms cannot express (invite, pin, react, change info) would stay
		// revoked and the "lifted" user would still be half-muted.
		if err := h.port.UnrestrictMember(ctx, inc.ChatID, inc.UserID); err != nil {
			return "", fmt.Errorf("unmute user %d in chat %d: %w", inc.UserID, inc.ChatID, err)
		}
		return "unmuted", nil
	default:
		return "", nil
	}
}

// train feeds the incident's captured tokens to the Bayes trainer under the
// given label and reports a short phrase for the reply. Best-effort by
// design: a missing token row (incident predating capture, captionless
// media, or an already-reviewed incident) and a trainer error both degrade
// to "not trained" rather than failing the admin's action.
func (h *Handler) train(incidentID int64, label string) string {
	if h.trainer == nil {
		return ""
	}
	tokens, ok, err := h.db.GetIncidentTokens(incidentID)
	if err != nil || !ok {
		return "not trained"
	}
	if err := h.trainer(string(domain.ScopeGlobal), label, tokens); err != nil {
		return "not trained"
	}
	h.dropTokens(incidentID)
	return "trained " + label
}

// dropTokens deletes the incident's captured tokens once it has been
// reviewed. Errors are ignored: the periodic prune is the backstop, and
// failing an admin action over bookkeeping would be worse than a stale row.
func (h *Handler) dropTokens(incidentID int64) {
	_ = h.db.DeleteIncidentTokens(incidentID)
}

// deleteEvidence removes the copies the bot posted to the admin chat and
// forgets them, returning how many messages were deleted. The bookkeeping
// rows are dropped only after Telegram accepted the delete, so a failed
// call leaves the evidence discoverable instead of silently orphaned.
func (h *Handler) deleteEvidence(ctx context.Context, incidentID int64) (int, error) {
	adminChatID, ids, err := h.db.ListEvidence(incidentID)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := h.port.DeleteMessages(ctx, adminChatID, ids); err != nil {
		return 0, fmt.Errorf("delete evidence in chat %d: %w", adminChatID, err)
	}
	if err := h.db.DeleteEvidenceRows(incidentID); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// joinReply assembles the callback toast from the decision and any optional
// side effects, keeping it short enough for Telegram's callback answer.
func joinReply(decision, lifted, trained string) string {
	parts := []string{decision}
	if lifted != "" {
		parts = append(parts, lifted+" (deleted messages cannot be restored)")
	}
	if trained != "" {
		parts = append(parts, trained)
	}
	return strings.Join(parts, "; ")
}
