package telegram

import (
	"sync"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

type albumKey struct {
	chat  int64
	group string
}

// AlbumBuffer coalesces album parts sharing (chat_id, media_group_id) that
// Telegram delivers as separate updates, flushing them together after a window.
type AlbumBuffer struct {
	mu        sync.Mutex
	window    time.Duration
	flush     func(parts []domain.Message)
	pending   map[albumKey][]domain.Message
	timers    map[albumKey]*time.Timer
	now       func() time.Time
	afterFunc func(time.Duration, func()) *time.Timer
}

func NewAlbumBuffer(window time.Duration, flush func(parts []domain.Message)) *AlbumBuffer {
	return &AlbumBuffer{
		window:    window,
		flush:     flush,
		pending:   map[albumKey][]domain.Message{},
		timers:    map[albumKey]*time.Timer{},
		now:       time.Now,
		afterFunc: time.AfterFunc,
	}
}

// Add buffers album parts and returns false for them; returns true for a
// standalone message the caller should handle immediately.
func (a *AlbumBuffer) Add(m domain.Message) bool {
	if m.MediaGroupID == "" {
		return true
	}
	k := albumKey{m.ChatID, m.MediaGroupID}
	a.mu.Lock()
	first := len(a.pending[k]) == 0
	a.pending[k] = append(a.pending[k], m)
	if first {
		a.timers[k] = a.afterFunc(a.window, func() { a.fire(k) })
	}
	a.mu.Unlock()
	return false
}

func (a *AlbumBuffer) fire(k albumKey) {
	a.mu.Lock()
	parts := a.pending[k]
	delete(a.pending, k)
	delete(a.timers, k)
	a.mu.Unlock()
	if len(parts) > 0 {
		a.flush(parts)
	}
}

// Stop cancels all pending timers and flushes whatever parts each group had
// buffered so far (call at shutdown). Without this, album parts that never
// reach their window before shutdown would otherwise be silently dropped —
// already marked seen via MarkUpdateSeen, so a restart would never see them
// again either.
func (a *AlbumBuffer) Stop() {
	a.mu.Lock()
	pending := a.pending
	a.pending = map[albumKey][]domain.Message{}
	for _, t := range a.timers {
		t.Stop()
	}
	a.timers = map[albumKey]*time.Timer{}
	a.mu.Unlock()
	for _, parts := range pending {
		if len(parts) > 0 {
			a.flush(parts)
		}
	}
}
