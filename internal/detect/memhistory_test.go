package detect

import (
	"sync"
	"testing"
	"time"
)

// fakeClock provides a deterministic, manually-advanceable time source for tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestMemHistory_RecordAndCountDup_WithinWindow(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 10 * time.Second

	if got := h.RecordAndCountDup(1, 2, "hash-a", window); got != 1 {
		t.Fatalf("first record: got %d, want 1", got)
	}
	if got := h.RecordAndCountDup(1, 2, "hash-a", window); got != 2 {
		t.Fatalf("second record: got %d, want 2", got)
	}
	if got := h.RecordAndCountDup(1, 2, "hash-a", window); got != 3 {
		t.Fatalf("third record: got %d, want 3", got)
	}
}

func TestMemHistory_RecordAndCountDup_ResetsAfterWindow(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 10 * time.Second

	for i := 0; i < 3; i++ {
		h.RecordAndCountDup(1, 2, "hash-a", window)
	}

	// Advance clock past the window; the old events should be pruned.
	clock.Advance(window + time.Second)

	if got := h.RecordAndCountDup(1, 2, "hash-a", window); got != 1 {
		t.Fatalf("after window: got %d, want 1", got)
	}
}

func TestMemHistory_RecordAndCountDup_DifferentHashesIndependent(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 10 * time.Second

	h.RecordAndCountDup(1, 2, "hash-a", window)
	h.RecordAndCountDup(1, 2, "hash-a", window)
	if got := h.RecordAndCountDup(1, 2, "hash-b", window); got != 1 {
		t.Fatalf("different hash: got %d, want 1", got)
	}
}

func TestMemHistory_RecordAndCountDup_DifferentUsersIndependent(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 10 * time.Second

	h.RecordAndCountDup(1, 2, "hash-a", window)
	h.RecordAndCountDup(1, 2, "hash-a", window)
	if got := h.RecordAndCountDup(1, 3, "hash-a", window); got != 1 {
		t.Fatalf("different user: got %d, want 1", got)
	}
	if got := h.RecordAndCountDup(9, 2, "hash-a", window); got != 1 {
		t.Fatalf("different chat: got %d, want 1", got)
	}
}

func TestMemHistory_RecentShortCount_WithinWindow(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 5 * time.Second

	if got := h.RecentShortCount(1, 2, window); got != 1 {
		t.Fatalf("first short: got %d, want 1", got)
	}
	if got := h.RecentShortCount(1, 2, window); got != 2 {
		t.Fatalf("second short: got %d, want 2", got)
	}
}

func TestMemHistory_RecentShortCount_ResetsAfterWindow(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 5 * time.Second

	h.RecentShortCount(1, 2, window)
	h.RecentShortCount(1, 2, window)

	clock.Advance(window + time.Second)

	if got := h.RecentShortCount(1, 2, window); got != 1 {
		t.Fatalf("after window: got %d, want 1", got)
	}
}

func TestMemHistory_DupAndShortAreIndependentCounters(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 5 * time.Second

	// A message that is both a duplicate and short calls both entry points.
	if got := h.RecordAndCountDup(1, 2, "hash-a", window); got != 1 {
		t.Fatalf("dup count: got %d, want 1", got)
	}
	if got := h.RecentShortCount(1, 2, window); got != 1 {
		t.Fatalf("short count: got %d, want 1", got)
	}
	// Dup count should be unaffected by the short-event recording.
	if got := h.RecordAndCountDup(1, 2, "hash-a", window); got != 2 {
		t.Fatalf("dup count after short: got %d, want 2", got)
	}
}

func TestMemHistory_Sweep_DropsOldEvents(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := NewMemHistory()
	h.now = clock.Now

	window := 100 * time.Second

	h.RecordAndCountDup(1, 2, "hash-a", window)
	h.RecentShortCount(1, 2, window)

	clock.Advance(50 * time.Second)
	h.Sweep(10 * time.Second)

	// Events are older than maxAge=10s (50s old), so Sweep should have
	// dropped them; a fresh count within the (still valid) window is 1.
	if got := h.RecordAndCountDup(1, 2, "hash-a", window); got != 1 {
		t.Fatalf("after sweep: got %d, want 1", got)
	}
}

func TestMemHistory_ImplementsHistoryInterface(t *testing.T) {
	var _ History = NewMemHistory()
}

func TestMemHistory_ConcurrentAccess(t *testing.T) {
	h := NewMemHistory()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			chatID := int64(n % 5)
			userID := int64(n % 7)
			h.RecordAndCountDup(chatID, userID, "hash", time.Second)
			h.RecentShortCount(chatID, userID, time.Second)
			h.Sweep(time.Minute)
		}(i)
	}
	wg.Wait()
}
