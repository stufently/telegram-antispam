// Package admin handles admin-chat inline button presses (the evidence
// message's action row). Every action here is destructive or
// learning-affecting, so RBAC — Authorized — gates every branch of Handle:
// no store or Telegram side effect may run before it returns true.
package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	act, key, ok := ParseCallback(cb.Data)
	if !ok {
		return h.port.AnswerCallback(ctx, cb.ID, "invalid callback")
	}

	incidentID, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return h.port.AnswerCallback(ctx, cb.ID, "invalid incident")
	}

	sourceChatID, err := h.db.GetIncidentChat(incidentID)
	if err != nil {
		return fmt.Errorf("lookup incident %d source chat: %w", incidentID, err)
	}

	authorized, err := h.Authorized(ctx, sourceChatID, cb.PresserID)
	if err != nil {
		return fmt.Errorf("authorize presser %d: %w", cb.PresserID, err)
	}
	if !authorized {
		// Reject: no action branch below this point runs for an
		// unauthorized presser.
		return h.port.AnswerCallback(ctx, cb.ID, "not authorized")
	}

	text, err := h.dispatch(act, key)
	if err != nil {
		return fmt.Errorf("dispatch %s: %w", act, err)
	}
	return h.port.AnswerCallback(ctx, cb.ID, text)
}

// dispatch applies the per-action effect. For M2 these are minimal: the
// store-side sample writes are stubs (origin "user"), and full moderation
// actions (unban/unrestrict) land in a later milestone. Every call here is
// only reached once Handle has confirmed Authorized == true.
func (h *Handler) dispatch(act Action, incidentKey string) (string, error) {
	switch act {
	case ActFalsePositive:
		if err := h.db.InsertSample("incident", string(act), "user", incidentKey); err != nil {
			return "", err
		}
		return "marked false positive", nil
	case ActConfirmSpam:
		if err := h.db.InsertSample("incident", string(act), "user", incidentKey); err != nil {
			return "", err
		}
		return "confirmed spam", nil
	case ActLiftNoLearn:
		// Explicitly not a learning signal: no sample write.
		return "lifted (not learned)", nil
	case ActDeleteEvidence:
		return "evidence marked for deletion", nil
	default:
		return "unknown action", nil
	}
}
