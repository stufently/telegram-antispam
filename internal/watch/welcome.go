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
)

// WelcomeStore is the persistence Welcomer needs. *store.DB satisfies it.
// GetChat's row is only consulted for Enabled; dry-run is intentionally
// ignored because a greeting is not a sanction.
type WelcomeStore interface {
	WasWelcomed(chatID, userID int64) (bool, error)
	MarkWelcomed(chatID, userID int64) error
	TrustCount(chatID, userID int64) (int, error)
	GetChat(chatID int64) (store.ChatRow, bool, error)
}

// Welcomer sends one ephemeral greeting per (chat, user). Config is read on
// every event, so a reload turns the option on or off without a restart.
type Welcomer struct {
	Config    *config.Store
	Store     WelcomeStore
	Port      telegram.Port
	Blocklist detect.BlocklistSource
	Now       func() time.Time

	mu   sync.Mutex
	sent map[int64][]time.Time
}

// Observe decides whether ev should be greeted and, when it should, sends
// the text. A store read error returns OutcomeError and sends nothing.
// A failed send is not recorded, so a later event can try again.
func (w *Welcomer) Observe(ctx context.Context, ev telegram.JoinEvent) (Outcome, error) {
	if w.Config == nil {
		return OutcomeError, errors.New("welcome: no config")
	}
	cfg := w.Config.Current()
	if cfg == nil {
		return OutcomeError, errors.New("welcome: no config")
	}
	if !telegram.RegisteredChat(cfg, ev.ChatID) {
		return OutcomeSkipNotAdmitted, nil
	}
	text, on := cfg.Welcome.For(ev.ChatID)
	if !on {
		return OutcomeSkipDisabled, nil
	}
	row, found, err := w.Store.GetChat(ev.ChatID)
	if err != nil {
		return OutcomeError, err
	}
	if found && !row.Enabled {
		return OutcomeSkipChatDisabled, nil
	}
	if w.Blocklist != nil && w.Blocklist.Listed(ev.UserID) {
		return OutcomeSkipBlocklisted, nil
	}
	known, err := w.Store.WasWelcomed(ev.ChatID, ev.UserID)
	if err != nil {
		return OutcomeError, err
	}
	if known {
		return OutcomeSkipKnown, nil
	}
	trust, err := w.Store.TrustCount(ev.ChatID, ev.UserID)
	if err != nil {
		return OutcomeError, err
	}
	if trust > 0 {
		return OutcomeSkipKnown, nil
	}
	max := 20
	if cfg.Welcome.MaxPerMinute != nil {
		max = *cfg.Welcome.MaxPerMinute
	}
	now := w.now()
	if w.rateCapped(ev.ChatID, now, max) {
		return OutcomeSkipRateCapped, nil
	}
	if _, err := w.Port.SendWelcome(ctx, ev.ChatID, ev.UserID, text); err != nil {
		return OutcomeError, err
	}
	w.recordSend(ev.ChatID, now)
	if err := w.Store.MarkWelcomed(ev.ChatID, ev.UserID); err != nil {
		return OutcomeError, err
	}
	return OutcomeSent, nil
}

func (w *Welcomer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// welcomeWindow is the fixed rolling minute the per-chat cap counts.
const welcomeWindow = 60 * time.Second

func (w *Welcomer) rateCapped(chatID int64, now time.Time, max int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.prune(chatID, now)) >= max
}

func (w *Welcomer) recordSend(chatID int64, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sent == nil {
		w.sent = map[int64][]time.Time{}
	}
	w.sent[chatID] = append(w.prune(chatID, now), now)
}

// prune returns the send times for chatID that are still inside the window.
// Caller holds w.mu. The result is a fresh slice, so appending to it does
// not alias the stored one.
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
