// Package recent remembers which messages each sender posted in each chat
// recently, so a delete_mute can also remove the sender's earlier messages
// (a ban gets that from Telegram's revoke_messages; a mute does not).
//
// Only (chat, sender, message id, time) is kept — never text — and only in
// memory: a restart forgets the last window, which costs at most one missed
// cleanup and keeps message metadata out of the database.
package recent

import (
	"sort"
	"sync"
	"time"
)

// MaxPerSender bounds one sender's entries. It equals Telegram's
// deleteMessages limit, so the result always fits one call.
const MaxPerSender = 100

type key struct{ chat, user int64 }

type entry struct {
	id int
	at time.Time
}

// Buffer is safe for concurrent use: chats are moderated concurrently.
type Buffer struct {
	window func() time.Duration
	now    func() time.Time

	mu        sync.Mutex
	m         map[key][]entry
	lastSweep time.Time
}

// New returns a buffer that retains window() worth of messages. window is a
// func so a config reload applies without rebuilding the buffer; a
// non-positive value records nothing.
func New(window func() time.Duration) *Buffer {
	return &Buffer{window: window, now: time.Now, m: make(map[key][]entry)}
}

// SetClock replaces time.Now, for tests.
func (b *Buffer) SetClock(now func() time.Time) { b.now = now }

// Record notes that user posted messageID in chat now. A repeat of an id
// already held (an album flushed twice, a reprocessed update) is ignored.
func (b *Buffer) Record(chat, user int64, messageID int) {
	w := b.window()
	if w <= 0 || user == 0 || messageID == 0 {
		return
	}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key{chat, user}
	es := prune(b.m[k], now.Add(-w))
	for _, e := range es {
		if e.id == messageID {
			b.m[k] = es
			return
		}
	}
	es = append(es, entry{id: messageID, at: now})
	if len(es) > MaxPerSender {
		es = es[len(es)-MaxPerSender:]
	}
	b.m[k] = es
	// Senders who post once and leave would otherwise stay forever.
	if now.Sub(b.lastSweep) >= w {
		b.lastSweep = now
		cutoff := now.Add(-w)
		for kk, v := range b.m {
			if v = prune(v, cutoff); len(v) == 0 {
				delete(b.m, kk)
			} else {
				b.m[kk] = v
			}
		}
	}
}

// Since returns user's message ids in chat recorded at or after since, in
// increasing order.
func (b *Buffer) Since(chat, user int64, since time.Time) []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ids []int
	for _, e := range b.m[key{chat, user}] {
		if !e.at.Before(since) {
			ids = append(ids, e.id)
		}
	}
	sort.Ints(ids)
	return ids
}

// prune drops entries older than cutoff. Entries are appended in time order.
func prune(es []entry, cutoff time.Time) []entry {
	i := 0
	for i < len(es) && es[i].at.Before(cutoff) {
		i++
	}
	if i == 0 {
		return es
	}
	return append([]entry(nil), es[i:]...)
}
