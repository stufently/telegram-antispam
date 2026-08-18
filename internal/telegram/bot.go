package telegram

import (
	"context"
	"log"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

// RegisteredChat reports whether the bot should serve chatID under cfg.
func RegisteredChat(cfg *config.Config, chatID int64) bool {
	switch cfg.Chats.Mode {
	case "allowlist":
		for _, id := range cfg.Chats.Allowlist {
			if id == chatID {
				return true
			}
		}
		return false
	default: // auto, owners_only (owners handled at registration time)
		return true
	}
}

// ImmuneSender reports senders the moderation pipeline must never act on.
func ImmuneSender(s domain.Sender) bool {
	return s.Kind == domain.SenderAnonAdmin || s.Kind == domain.SenderLinkedChannel
}

// IncidentMachine is the subset of *incident.Machine the Handler holds.
//
// It is declared here, in package telegram, rather than imported from
// package incident, because package incident already imports package
// telegram (for its Port dependency): if this package also imported
// incident for the concrete *incident.Machine type, the two packages would
// import each other, which Go disallows. *incident.Machine satisfies this
// interface structurally, so no import is needed — the caller (main) passes
// a *incident.Machine in directly. Same consumer-declares-the-interface
// pattern already used for store.DB satisfying incident.Repo.
type IncidentMachine interface {
	Handle(ctx context.Context, inc domain.Incident) error
}

// Handler is the M1 delivery spine: dedup, route, register chat. Detection and
// verdict-building arrive in later milestones; the incident machine is wired
// but invoked by those milestones.
type Handler struct {
	db      *store.DB
	seq     *Sequencer
	cfg     *config.Store
	machine IncidentMachine
}

func NewHandler(db *store.DB, seq *Sequencer, cfg *config.Store, machine IncidentMachine) *Handler {
	return &Handler{db: db, seq: seq, cfg: cfg, machine: machine}
}

// OnMessage is called by the poller for each message update.
func (h *Handler) OnMessage(ctx context.Context, updateID int64, m domain.Message) {
	fresh, err := h.db.MarkUpdateSeen(updateID)
	if err != nil {
		log.Printf("dedup update %d: %v", updateID, err)
		return
	}
	if !fresh {
		return
	}
	cfg := h.cfg.Current()
	if !RegisteredChat(cfg, m.ChatID) || ImmuneSender(m.Sender) {
		return
	}
	h.seq.Submit(m.ChatID, func() {
		if err := h.db.RegisterChat(m.ChatID, cfg.Chats.StartInDryRun); err != nil {
			log.Printf("register chat %d: %v", m.ChatID, err)
		}
		log.Printf("chat=%d msg=%d sender=%s: observed (dry-run spine)", m.ChatID, m.MessageID, m.Sender.Kind)
	})
}
