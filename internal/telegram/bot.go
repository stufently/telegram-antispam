package telegram

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

// albumWindow is how long AlbumBuffer waits for further parts of a media
// group before flushing them as one incident.
const albumWindow = 700 * time.Millisecond

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

// Handler is the delivery spine: dedup, route, register chat, and — once
// decide reports an actionable verdict — drive the incident machine.
// Detection itself (the decide cascade) arrives in M3; until then decide
// defaults to "no verdict", which keeps M1 behavior (register + log).
type Handler struct {
	db      *store.DB
	seq     *Sequencer
	cfg     *config.Store
	machine IncidentMachine
	album   *AlbumBuffer

	// decide is the verdict source. M2 has no detector; the default always
	// returns (Verdict{}, false), so OnMessage/OnEditedMessage keep M1
	// behavior. M3 replaces it with the real detection cascade via
	// SetDecide.
	decide func(domain.Message) (domain.Verdict, bool)

	// rootCtx is used for work that runs off the AlbumBuffer's own timer
	// goroutine (flushAlbum), which has no request context of its own. It
	// defaults to context.Background() so tests need not set it, but main
	// overrides it via SetContext with the process's shutdown-aware ctx: an
	// album flush that fires during shutdown must observe cancellation
	// (LivePort's submitSync blocks on <-ctx.Done()), or it would hang
	// forever if the dispatcher has already stopped consuming jobs.
	rootCtx context.Context
}

func NewHandler(db *store.DB, seq *Sequencer, cfg *config.Store, machine IncidentMachine) *Handler {
	h := &Handler{
		db:      db,
		seq:     seq,
		cfg:     cfg,
		machine: machine,
		decide:  func(domain.Message) (domain.Verdict, bool) { return domain.Verdict{}, false },
		rootCtx: context.Background(),
	}
	h.album = NewAlbumBuffer(albumWindow, h.flushAlbum)
	return h
}

// SetDecide overrides the verdict hook. It is exported so main can wire in
// the real detection cascade (M3) and so tests can inject a verdict without
// package telegram importing package incident, which would create an
// import cycle (incident already imports telegram for its Port dependency).
func (h *Handler) SetDecide(decide func(domain.Message) (domain.Verdict, bool)) {
	h.decide = decide
}

// SetContext sets the context used for work triggered off the AlbumBuffer's
// own timer goroutine (see rootCtx). Call it once during setup, before any
// updates can arrive.
func (h *Handler) SetContext(ctx context.Context) {
	h.rootCtx = ctx
}

// Stop releases Handler-owned background resources (the album buffer's
// pending flush timers). Call it during shutdown.
func (h *Handler) Stop() {
	h.album.Stop()
}

// OnMessage is called by the poller for each message update.
func (h *Handler) OnMessage(ctx context.Context, updateID int64, m domain.Message) {
	h.onUpdate(ctx, updateID, m)
}

// OnEditedMessage is called by the poller for each edited_message update; it
// runs the same pipeline as OnMessage.
func (h *Handler) OnEditedMessage(ctx context.Context, updateID int64, m domain.Message) {
	h.onUpdate(ctx, updateID, m)
}

func (h *Handler) onUpdate(ctx context.Context, updateID int64, m domain.Message) {
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
	// Standalone messages (Add returns true) are handled immediately; album
	// parts are buffered and flushed together as one incident.
	if h.album.Add(m) {
		h.seq.Submit(m.ChatID, func() {
			h.process(ctx, []domain.Message{m})
		})
	}
}

// flushAlbum is the AlbumBuffer's flush callback: it runs on the buffer's
// own timer goroutine, outside any request context, so it submits its own
// sequencer job using rootCtx (see the Handler field doc).
func (h *Handler) flushAlbum(parts []domain.Message) {
	if len(parts) == 0 {
		return
	}
	chatID := parts[0].ChatID
	h.seq.Submit(chatID, func() {
		h.process(h.rootCtx, parts)
	})
}

// process runs on the per-chat sequencer worker for parts (one standalone
// message, or all parts of one flushed album): register the chat, then
// either build and hand off one incident (decide reports an actionable
// verdict) or fall back to the M1 observe-and-log behavior.
func (h *Handler) process(ctx context.Context, parts []domain.Message) {
	if len(parts) == 0 {
		return
	}
	first := parts[0]
	cfg := h.cfg.Current()
	if err := h.db.RegisterChat(first.ChatID, cfg.Chats.StartInDryRun); err != nil {
		log.Printf("register chat %d: %v", first.ChatID, err)
	}

	verdict, ok := h.decide(first)
	if !ok || !verdict.IsActionable() {
		for _, m := range parts {
			log.Printf("chat=%d msg=%d sender=%s: observed (dry-run spine)", m.ChatID, m.MessageID, m.Sender.Kind)
		}
		return
	}

	dryRun := cfg.Chats.StartInDryRun
	if row, found, err := h.db.GetChat(first.ChatID); err != nil {
		log.Printf("lookup chat %d dry-run: %v", first.ChatID, err)
	} else if found {
		if !row.Enabled {
			// An admin explicitly disabled this chat (DisableChat); an
			// actionable verdict must not reach the incident machine.
			return
		}
		dryRun = row.DryRun
	}

	ids := make([]int, len(parts))
	for i, m := range parts {
		ids[i] = m.MessageID
	}
	// Telegram's copyMessages requires message_ids in strictly increasing
	// order; album parts aren't guaranteed to arrive (and so be appended to
	// the buffer) in id order.
	sort.Ints(ids)

	inc := domain.Incident{
		ChatID:     first.ChatID,
		MessageIDs: ids,
		ThreadID:   first.ThreadID,
		Sender:     first.Sender,
		Verdict:    verdict,
		DryRun:     dryRun,
	}
	if err := h.machine.Handle(ctx, inc); err != nil {
		log.Printf("chat=%d incident: %v", first.ChatID, err)
	}
}
