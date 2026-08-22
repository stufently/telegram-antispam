package telegram

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/detect"
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

	// decide is the verdict source for OnMessage (and for OnEditedMessage
	// when editedDecide is nil, see below). M2 has no detector; the default
	// always returns (Verdict{}, false), so OnMessage/OnEditedMessage keep
	// M1 behavior. M3 replaces it with the real detection cascade via
	// SetDecide.
	decide func(domain.Message) (domain.Verdict, bool)

	// editedDecide, when set via SetEditedDecide, is the verdict source for
	// OnEditedMessage instead of decide. It exists so the wiring in main
	// can pass edited=true into the cascade (detect.Cascade.Decide takes an
	// edited bool) for the edited path while decide passes edited=false for
	// the OnMessage path. Left nil, OnEditedMessage falls back to decide,
	// which keeps pre-M3 behavior (and existing tests that only call
	// SetDecide) working unchanged.
	editedDecide func(domain.Message) (domain.Verdict, bool)

	// commands, when set, handles moderator commands typed in a moderated
	// chat (/spam, /ham). It is an interface rather than a function so the
	// match and the execution stay one object: matching must be cheap and
	// synchronous (it runs on the update path), while execution belongs on
	// the per-chat sequencer.
	commands CommandHandler

	// rootCtx is the lifecycle context for every job accepted by the per-chat
	// sequencer, including work flushed by AlbumBuffer. It is deliberately
	// independent from the short-lived update callback context, so accepted
	// work can drain during shutdown. Main cancels it after the bounded drain
	// deadline and only then stops the outbound dispatcher.
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

// SetEditedDecide overrides the verdict hook used for OnEditedMessage,
// distinct from SetDecide's hook (used for OnMessage). It exists so main
// can wire cascade.Decide(m, true) for edits — the FlagEdits behavioral
// check depends on knowing a message is an edit — while SetDecide wires
// cascade.Decide(m, false) for new messages. If never called,
// OnEditedMessage falls back to the SetDecide hook.
func (h *Handler) SetEditedDecide(decide func(domain.Message) (domain.Verdict, bool)) {
	h.editedDecide = decide
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
	h.onUpdate(ctx, updateID, m, false)
}

// OnEditedMessage is called by the poller for each edited_message update; it
// runs the same pipeline as OnMessage, but marks the message as edited so
// the edited-specific decide hook (see editedDecide) and behavioral checks
// (e.g. FlagEdits) see it as such.
func (h *Handler) OnEditedMessage(ctx context.Context, updateID int64, m domain.Message) {
	h.onUpdate(ctx, updateID, m, true)
}

func (h *Handler) onUpdate(_ context.Context, updateID int64, m domain.Message, edited bool) {
	fresh, err := h.db.MarkUpdateSeen(updateID)
	if err != nil {
		log.Printf("dedup update %d: %v", updateID, err)
		return
	}
	if !fresh {
		return
	}
	cfg := h.cfg.Current()
	if !RegisteredChat(cfg, m.ChatID) {
		return
	}
	// Moderator commands are dispatched BEFORE the immunity filter, and the
	// order is load-bearing: an anonymous administrator posts as the chat
	// itself, which is precisely the sender kind ImmuneSender drops. Checked
	// after the filter, /spam from a chat's owner would be silently ignored.
	// Edits are excluded: re-editing a message into "/spam" would replay a
	// destructive action that was already performed (or refused) once.
	if !edited && h.commands != nil && h.commands.Match(m) {
		h.seq.Submit(m.ChatID, func() {
			h.commands.Handle(h.rootCtx, m)
		})
		return
	}
	if ImmuneSender(m.Sender) {
		return
	}
	// Standalone messages (Add returns true) are handled immediately; album
	// parts are buffered and flushed together as one incident.
	if h.album.Add(m) {
		h.seq.Submit(m.ChatID, func() {
			h.process(h.rootCtx, []domain.Message{m}, edited)
		})
	}
}

// flushAlbum is the AlbumBuffer's flush callback: it runs on the buffer's
// own timer goroutine, outside any request context, so it submits its own
// sequencer job using rootCtx (see the Handler field doc). Album grouping
// only applies to freshly-sent media groups, never to edits, so parts here
// are always treated as not edited.
func (h *Handler) flushAlbum(parts []domain.Message) {
	if len(parts) == 0 {
		return
	}
	chatID := parts[0].ChatID
	h.seq.Submit(chatID, func() {
		h.process(h.rootCtx, parts, false)
	})
}

// process runs on the per-chat sequencer worker for parts (one standalone
// message, or all parts of one flushed album): register the chat, then
// either build and hand off one incident (decide reports an actionable
// verdict) or fall back to the M1 observe-and-log behavior. It also runs
// the trust bump for non-actionable meaningful messages (see below) — this
// method always runs inside a sequencer job, i.e. off the single poll
// goroutine, which is why the bump lives here rather than in onUpdate.
func (h *Handler) process(ctx context.Context, parts []domain.Message, edited bool) {
	if len(parts) == 0 {
		return
	}
	first := parts[0]
	// The part that carries the album's text. An album's caption belongs to
	// exactly ONE of its items and NOT necessarily the first — Telegram lets
	// the sender attach it to any of them, and the parts arrive as separate
	// updates in no guaranteed order. Judging parts[0] unconditionally meant
	// a spam album whose caption sat on the second photo was scored as a
	// captionless one: empty text, nothing for the rules, Bayes or the LLM to
	// see, verdict "clean".
	//
	// Deliberately ONE part is judged, not all of them: Decide feeds the
	// duplicate/short-message windows as a side effect, so scoring every item
	// would turn a single five-photo album into five flood events.
	judged := first
	for _, m := range parts {
		if strings.TrimSpace(m.Text) != "" {
			judged = m
			break
		}
	}
	cfg := h.cfg.Current()
	if err := h.db.RegisterChat(first.ChatID, cfg.Chats.DryRunDefault()); err != nil {
		log.Printf("register chat %d: %v", first.ChatID, err)
	}

	decide := h.decide
	if edited && h.editedDecide != nil {
		decide = h.editedDecide
	}
	verdict, ok := decide(judged)
	if !ok || !verdict.IsActionable() {
		// A non-actionable, meaningful message from a real user counts
		// toward that user's trust score (the M3 cascade's trust gate
		// reads this back via detect.TrustSource). This is a wiring-level
		// side effect, not part of the pure cascade: detect.Cascade.Decide
		// never bumps trust itself.
		// Edits deliberately do not earn trust. The counter is meant to
		// measure participation, and an edit is not a new message: editing
		// one message five times used to raise trust to 5 and buy a brand
		// new account its way past the link policy, the fake-admin check and
		// Bayes — all of which only apply to the untrusted.
		if !edited && verdict.Reason != detect.ReasonAdminLookupUnavailable && judged.Sender.Kind == domain.SenderUser && detect.IsMeaningful(detect.Normalize(judged)) {
			if _, err := h.db.BumpTrust(first.ChatID, first.Sender.UserID); err != nil {
				log.Printf("bump trust chat=%d user=%d: %v", first.ChatID, first.Sender.UserID, err)
			}
		}
		if verdict.Reason == detect.ReasonAdminLookupUnavailable {
			log.Printf("chat=%d msg=%d: moderation deferred: admin lookup unavailable", first.ChatID, first.MessageID)
			return
		}
		// The signals carry the cascade's numbers (Bayes ratio vs threshold),
		// never message text — that is what makes a later "why did this pass?"
		// answerable at all. See detect.Cascade.Decide.
		for _, m := range parts {
			log.Printf("chat=%d msg=%d sender=%s: observed [%s]", m.ChatID, m.MessageID, m.Sender.Kind, formatSignals(verdict.Signals))
		}
		return
	}

	dryRun := cfg.Chats.DryRunDefault()
	if row, found, err := h.db.GetChat(first.ChatID); err != nil {
		// Fail closed: an unreadable Enabled/DryRun gate must not fall
		// through to acting with a guessed default (which could act live
		// against a chat the admin actually disabled or is running dry-run).
		log.Printf("lookup chat %d dry-run: %v", first.ChatID, err)
		return
	} else if found {
		if !row.Enabled {
			// An admin explicitly disabled this chat (DisableChat); an
			// actionable verdict must not reach the incident machine.
			return
		}
		dryRun = row.DryRun
	}
	// Config has the last word: the stored row says where the chat started,
	// chats.enforce / chats.force_dry_run say where the operator wants it
	// now. Resolved after the fail-closed read above, so an unreadable gate
	// still stops moderation rather than being overridden into acting.
	dryRun = cfg.Chats.DryRunFor(first.ChatID, dryRun)

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
		// Tokens travel with the incident because the originals are deleted
		// at the end of Handle: without capturing them here, an admin's
		// later Confirm-spam / False-positive press would have nothing to
		// train on (see store.SaveIncidentTokens for what is and is not kept).
		Tokens: detect.Tokenize(detect.Normalize(judged)),
	}
	if err := h.machine.Handle(ctx, inc); err != nil {
		log.Printf("chat=%d incident: %v", first.ChatID, err)
	}
}

// formatSignals renders a verdict's signals for the log in a fixed, compact
// shape ("name=detail name=detail"). Signal details are cascade-produced
// diagnostics (scores, hosts), never raw user text, so this is safe to log.
func formatSignals(sigs []domain.Signal) string {
	if len(sigs) == 0 {
		return "no signals"
	}
	parts := make([]string, 0, len(sigs))
	for _, s := range sigs {
		if s.Detail == "" {
			parts = append(parts, s.Name)
			continue
		}
		parts = append(parts, s.Name+"="+s.Detail)
	}
	return strings.Join(parts, " ")
}

// CommandHandler executes moderator commands typed in a moderated chat.
// admin.Commands implements it; the interface lives here so package telegram
// does not import package admin (admin already imports telegram).
type CommandHandler interface {
	// Match reports whether the message is a command for this bot. It must
	// not perform I/O: it runs inline on the update path.
	Match(m domain.Message) bool
	// Handle executes the command. It runs on the chat's sequencer, so it is
	// serialized against that chat's moderation work.
	Handle(ctx context.Context, m domain.Message)
}

// SetCommands installs the moderator-command handler. Left unset, command
// messages take the ordinary detection path like any other text.
func (h *Handler) SetCommands(c CommandHandler) {
	h.commands = c
}
