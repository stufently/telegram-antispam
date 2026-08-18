package telegram

import (
	"sync"
	"testing"
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
