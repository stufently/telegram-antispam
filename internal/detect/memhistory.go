package detect

import (
	"sync"
	"time"
)

// historyEvent is a single recorded message event used for sliding-window counting.
type historyEvent struct {
	hash  string
	ts    time.Time
	short bool
}

// historyKey identifies a per-chat, per-user event bucket.
type historyKey struct {
	chatID int64
	userID int64
}

// MemHistory is an in-memory, sliding-window implementation of the History interface.
// It is safe for concurrent use.
type MemHistory struct {
	mu     sync.Mutex
	events map[historyKey][]historyEvent
	now    func() time.Time
}

// NewMemHistory returns a ready-to-use MemHistory backed by the wall clock.
func NewMemHistory() *MemHistory {
	return &MemHistory{
		events: make(map[historyKey][]historyEvent),
		now:    time.Now,
	}
}

var _ History = (*MemHistory)(nil)

// RecordAndCountDup records the current message's text hash and returns the count of
// events with the same hash within the given window, including the just-recorded one.
func (h *MemHistory) RecordAndCountDup(chatID, userID int64, textHash string, window time.Duration) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := historyKey{chatID: chatID, userID: userID}
	now := h.now()

	events := pruneEvents(h.events[key], now, window)
	events = append(events, historyEvent{hash: textHash, ts: now, short: false})
	h.events[key] = events

	count := 0
	for _, e := range events {
		if e.hash == textHash && withinWindow(e.ts, now, window) {
			count++
		}
	}
	return count
}

// RecentShortCount records a short-message event for (chatID, userID) and returns the
// count of short-message events within the given window, including the just-recorded one.
func (h *MemHistory) RecentShortCount(chatID, userID int64, window time.Duration) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := historyKey{chatID: chatID, userID: userID}
	now := h.now()

	events := pruneEvents(h.events[key], now, window)
	events = append(events, historyEvent{hash: "", ts: now, short: true})
	h.events[key] = events

	count := 0
	for _, e := range events {
		if e.short && withinWindow(e.ts, now, window) {
			count++
		}
	}
	return count
}

// Sweep drops all events older than maxAge across all keys, bounding memory usage.
// It is safe to call concurrently with the recording/counting methods.
func (h *MemHistory) Sweep(maxAge time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	for key, events := range h.events {
		pruned := pruneEvents(events, now, maxAge)
		if len(pruned) == 0 {
			delete(h.events, key)
			continue
		}
		h.events[key] = pruned
	}
}

// pruneEvents returns the subset of events whose timestamp is within window of now.
func pruneEvents(events []historyEvent, now time.Time, window time.Duration) []historyEvent {
	kept := events[:0:0]
	for _, e := range events {
		if withinWindow(e.ts, now, window) {
			kept = append(kept, e)
		}
	}
	return kept
}

// withinWindow reports whether ts is within window of now (i.e. now - ts <= window).
func withinWindow(ts, now time.Time, window time.Duration) bool {
	return now.Sub(ts) <= window
}
