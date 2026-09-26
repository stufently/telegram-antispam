package watch

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
)

// Outcome is the welcome decision, used as the result label of
// tg_antispam_welcome_total.
type Outcome string

const (
	OutcomeSkipNotAdmitted  Outcome = "skip_not_admitted"
	OutcomeSkipDisabled     Outcome = "skip_disabled"
	OutcomeSkipChatDisabled Outcome = "skip_chat_disabled"
	OutcomeSkipBlocklisted  Outcome = "skip_blocklisted"
	OutcomeSkipKnown        Outcome = "skip_known"
	OutcomeSkipRateCapped   Outcome = "skip_rate_capped"
	OutcomeSent             Outcome = "sent"
	OutcomeError            Outcome = "error"
	OutcomeQueued           Outcome = "queued"
)

type WelcomeTicket struct {
	ChatID, UserID int64
	Text           string
}

// WelcomeStore is what Welcomer reads. *store.DB satisfies it. GetChat is
// consulted only for Enabled; dry-run does not apply to a greeting.
type WelcomeStore interface {
	WasWelcomed(chatID, userID int64) (bool, error)
	MarkWelcomed(chatID, userID int64) error
	TrustCount(chatID, userID int64) (int, error)
	GetChat(chatID int64) (store.ChatRow, bool, error)
}

// Welcomer greets each (chat, user) once. It reads Config on every event,
// so a reload turns the option on or off without a restart.
type Welcomer struct {
	Config    *config.Store
	Store     WelcomeStore
	Port      telegram.Port
	Blocklist detect.BlocklistSource
	Now       func() time.Time

	mu       sync.Mutex
	sent     map[int64][]time.Time
	inflight map[int64]map[int64]struct{}
	wg       sync.WaitGroup
}

// Observe greets ev or explains why not. A store read error is OutcomeError
// and sends nothing. A failed send is not marked, so a later event can retry.
func (w *Welcomer) Observe(ctx context.Context, ev telegram.JoinEvent) (Outcome, error) {
	out, ticket, err := w.Admit(ctx, ev)
	if out != OutcomeQueued {
		return out, err
	}
	return w.Deliver(ctx, ticket)
}

func (w *Welcomer) Admit(ctx context.Context, ev telegram.JoinEvent) (Outcome, *WelcomeTicket, error) {
	if w.Config == nil {
		return OutcomeError, nil, errors.New("welcome: no config")
	}
	cfg := w.Config.Current()
	if cfg == nil {
		return OutcomeError, nil, errors.New("welcome: no config")
	}
	if !telegram.RegisteredChat(cfg, ev.ChatID) {
		return OutcomeSkipNotAdmitted, nil, nil
	}
	text, on := cfg.Welcome.For(ev.ChatID)
	if !on {
		return OutcomeSkipDisabled, nil, nil
	}
	row, found, err := w.Store.GetChat(ev.ChatID)
	if err != nil {
		return OutcomeError, nil, err
	}
	if found && !row.Enabled {
		return OutcomeSkipChatDisabled, nil, nil
	}
	if w.Blocklist != nil && w.Blocklist.Listed(ev.UserID) {
		return OutcomeSkipBlocklisted, nil, nil
	}
	known, err := w.Store.WasWelcomed(ev.ChatID, ev.UserID)
	if err != nil {
		return OutcomeError, nil, err
	}
	if known {
		return OutcomeSkipKnown, nil, nil
	}
	trust, err := w.Store.TrustCount(ev.ChatID, ev.UserID)
	if err != nil {
		return OutcomeError, nil, err
	}
	if trust > 0 {
		return OutcomeSkipKnown, nil, nil
	}
	max := 20
	if cfg.Welcome.MaxPerMinute != nil {
		max = *cfg.Welcome.MaxPerMinute
	}
	if out := w.reserve(ev.ChatID, ev.UserID, w.now(), max); out != OutcomeQueued {
		return out, nil, nil
	}
	return OutcomeQueued, &WelcomeTicket{ChatID: ev.ChatID, UserID: ev.UserID, Text: text}, nil
}

func (w *Welcomer) Deliver(ctx context.Context, t *WelcomeTicket) (Outcome, error) {
	if t == nil {
		return OutcomeError, errors.New("welcome: nil ticket")
	}
	_, sendErr := w.Port.SendWelcome(ctx, t.ChatID, t.UserID, t.Text)
	// Count the attempt when it finishes, not when it was reserved: a 429
	// retry can outlive the minute, and recording the start time would let
	// the next join through immediately. A failed attempt still counts —
	// ErrEphemeralNotHonored did publish — but the user is not marked, so
	// a later join retries once the window moves.
	w.finish(t.ChatID, t.UserID, w.now())
	if sendErr != nil {
		return OutcomeError, sendErr
	}
	if err := w.Store.MarkWelcomed(t.ChatID, t.UserID); err != nil {
		return OutcomeError, err
	}
	return OutcomeSent, nil
}

func (w *Welcomer) DeliverAsync(ctx context.Context, t *WelcomeTicket, done func(Outcome, error)) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		out, err := w.Deliver(ctx, t)
		if done != nil {
			done(out, err)
		}
	}()
}

func (w *Welcomer) Wait() { w.wg.Wait() }

func (w *Welcomer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// welcomeWindow is the fixed rolling minute the per-chat cap counts.
const welcomeWindow = 60 * time.Second

func (w *Welcomer) reserve(chatID, userID int64, now time.Time, max int) Outcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.inflight[chatID][userID]; ok {
		return OutcomeSkipKnown
	}
	if len(w.prune(chatID, now))+len(w.inflight[chatID]) >= max {
		return OutcomeSkipRateCapped
	}
	if w.inflight == nil {
		w.inflight = map[int64]map[int64]struct{}{}
	}
	if w.inflight[chatID] == nil {
		w.inflight[chatID] = map[int64]struct{}{}
	}
	w.inflight[chatID][userID] = struct{}{}
	return OutcomeQueued
}

func (w *Welcomer) finish(chatID, userID int64, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if users := w.inflight[chatID]; users != nil {
		delete(users, userID)
		if len(users) == 0 {
			delete(w.inflight, chatID)
		}
	}
	if w.sent == nil {
		w.sent = map[int64][]time.Time{}
	}
	w.sent[chatID] = append(w.prune(chatID, now), now)
}

// prune returns send times still inside the window. Caller holds w.mu.
// The result is a fresh slice, so the caller can append without aliasing.
func (w *Welcomer) prune(chatID int64, now time.Time) []time.Time {
	cutoff := now.Add(-welcomeWindow)
	var fresh []time.Time
	for _, ts := range w.sent[chatID] {
		if ts.After(cutoff) {
			fresh = append(fresh, ts)
		}
	}
	return fresh
}
