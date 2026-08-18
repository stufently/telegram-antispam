package detect

import (
	"sync"
	"time"
)

// dupEvent is a single recorded duplicate-detection event.
type dupEvent struct {
	hash string
	ts   time.Time
}

// shortEvent is a single recorded short-message event.
type shortEvent struct {
	ts time.Time
}

// historyKey identifies a per-chat, per-user event bucket.
type historyKey struct {
	chatID int64
	userID int64
}

// historyBucket holds the independent event slices for one (chat, user) pair.
// dup and short are pruned independently, each by its own window, so a
// smaller window on one counter never evicts still-valid events for the
// other.
type historyBucket struct {
	dup   []dupEvent
	short []shortEvent
}

// MemHistory is an in-memory, sliding-window implementation of the History interface.
// It is safe for concurrent use.
type MemHistory struct {
	mu      sync.Mutex
	buckets map[historyKey]*historyBucket
	now     func() time.Time
}

// NewMemHistory returns a ready-to-use MemHistory backed by the wall clock.
func NewMemHistory() *MemHistory {
	return &MemHistory{
		buckets: make(map[historyKey]*historyBucket),
		now:     time.Now,
	}
}

var _ History = (*MemHistory)(nil)

// RecordAndCountDup records the current message's text hash and returns the count of
// events with the same hash within the given window, including the just-recorded one.
// Only the dup slice is pruned/inspected here; short-message events for the same key
// are left untouched.
func (h *MemHistory) RecordAndCountDup(chatID, userID int64, textHash string, window time.Duration) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := historyKey{chatID: chatID, userID: userID}
	now := h.now()

	b := h.buckets[key]
	if b == nil {
		b = &historyBucket{}
		h.buckets[key] = b
	}

	b.dup = pruneDupEvents(b.dup, now, window)
	b.dup = append(b.dup, dupEvent{hash: textHash, ts: now})

	count := 0
	for _, e := range b.dup {
		if e.hash == textHash {
			count++
		}
	}
	return count
}

// RecentShortCount records a short-message event for (chatID, userID) and returns the
// count of short-message events within the given window, including the just-recorded one.
// Only the short slice is pruned/inspected here; duplicate-detection events for the same
// key are left untouched.
func (h *MemHistory) RecentShortCount(chatID, userID int64, window time.Duration) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := historyKey{chatID: chatID, userID: userID}
	now := h.now()

	b := h.buckets[key]
	if b == nil {
		b = &historyBucket{}
		h.buckets[key] = b
	}

	b.short = pruneShortEvents(b.short, now, window)
	b.short = append(b.short, shortEvent{ts: now})

	return len(b.short)
}

// Sweep drops all events older than maxAge across all keys, bounding memory usage.
// Both the dup and short slices of each bucket are pruned by maxAge; a key is removed
// entirely only once both slices are empty. It is safe to call concurrently with the
// recording/counting methods.
func (h *MemHistory) Sweep(maxAge time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	for key, b := range h.buckets {
		b.dup = pruneDupEvents(b.dup, now, maxAge)
		b.short = pruneShortEvents(b.short, now, maxAge)
		if len(b.dup) == 0 && len(b.short) == 0 {
			delete(h.buckets, key)
		}
	}
}

// pruneDupEvents returns the subset of dup events whose timestamp is within window of now.
// If nothing is stale, the original slice is returned unchanged (no allocation).
func pruneDupEvents(events []dupEvent, now time.Time, window time.Duration) []dupEvent {
	firstStale := -1
	for i, e := range events {
		if !withinWindow(e.ts, now, window) {
			firstStale = i
			break
		}
	}
	if firstStale == -1 {
		return events
	}

	kept := events[:0:0]
	for _, e := range events {
		if withinWindow(e.ts, now, window) {
			kept = append(kept, e)
		}
	}
	return kept
}

// pruneShortEvents returns the subset of short events whose timestamp is within window of now.
// If nothing is stale, the original slice is returned unchanged (no allocation).
func pruneShortEvents(events []shortEvent, now time.Time, window time.Duration) []shortEvent {
	firstStale := -1
	for i, e := range events {
		if !withinWindow(e.ts, now, window) {
			firstStale = i
			break
		}
	}
	if firstStale == -1 {
		return events
	}

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
