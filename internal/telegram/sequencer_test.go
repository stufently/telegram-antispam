package telegram

import (
	"sync"
	"testing"
	"time"
)

func TestSequencerOrdersPerChat(t *testing.T) {
	s := NewSequencer()
	var mu sync.Mutex
	seq := []int{}
	for i := 0; i < 100; i++ {
		i := i
		s.Submit(-100123, func() {
			mu.Lock()
			seq = append(seq, i)
			mu.Unlock()
		})
	}
	s.Wait()
	for i := range seq {
		if seq[i] != i {
			t.Fatalf("out of order at %d: %v", i, seq[:i+1])
		}
	}
}

func TestSequencerRunsDistinctChats(t *testing.T) {
	s := NewSequencer()
	done := make(chan int64, 2)
	s.Submit(-1, func() { done <- -1 })
	s.Submit(-2, func() { done <- -2 })
	s.Wait()
	close(done)
	got := map[int64]bool{}
	for id := range done {
		got[id] = true
	}
	if !got[-1] || !got[-2] {
		t.Fatalf("both chats should run, got %v", got)
	}
}

// TestSubmitNonBlockingOnFullBuffer fills one chat's queue (capacity 1024)
// while its worker is stalled on a gated job, then asserts a further Submit
// returns immediately (does not block the caller) and Dropped() reflects
// the drop, closing the M2-deferred finding that a full buffer could block
// the single poll goroutine.
func TestSubmitNonBlockingOnFullBuffer(t *testing.T) {
	s := NewSequencer()
	started := make(chan struct{})
	release := make(chan struct{})

	// Occupy the worker so nothing drains the queue while we fill it.
	s.Submit(-1, func() {
		close(started)
		<-release
	})
	<-started // the gated job is now running; the queue itself is empty

	// Fill the queue to its 1024 capacity; these all fit and must not block.
	for i := 0; i < 1024; i++ {
		s.Submit(-1, func() {})
	}

	if got := s.Dropped(); got != 0 {
		t.Fatalf("expected no drops before the queue is over capacity, got %d", got)
	}

	done := make(chan struct{})
	go func() {
		s.Submit(-1, func() {}) // queue is full: must not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked on a full buffer instead of dropping the job")
	}

	if got := s.Dropped(); got != 1 {
		t.Fatalf("expected Dropped() == 1, got %d", got)
	}

	close(release)
	s.Wait()
}

func TestSubmitAfterWaitIsNoop(t *testing.T) {
	s := NewSequencer()
	s.Wait() // shut down with no work
	ran := false
	// Must not panic; the job must not run.
	s.Submit(-1, func() { ran = true })
	s.Wait() // idempotent second Wait
	if ran {
		t.Fatal("job submitted after Wait should not run")
	}
}
