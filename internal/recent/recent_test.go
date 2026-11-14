package recent

import (
	"reflect"
	"testing"
	"time"
)

func newAt(window time.Duration, now *time.Time) *Buffer {
	b := New(func() time.Duration { return window })
	b.SetClock(func() time.Time { return *now })
	return b
}

func TestSinceFiltersByChatSenderAndTime(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	b := newAt(time.Hour, &now)
	b.Record(-1, 7, 10, time.Time{})
	now = now.Add(20 * time.Minute)
	b.Record(-1, 7, 12, time.Time{})
	b.Record(-1, 8, 11, time.Time{}) // other sender
	b.Record(-2, 7, 13, time.Time{}) // other chat
	b.Record(-1, 7, 12, time.Time{}) // repeat
	if got := b.Since(-1, 7, now.Add(-time.Hour)); !reflect.DeepEqual(got, []int{10, 12}) {
		t.Fatalf("got %v, want [10 12]", got)
	}
	if got := b.Since(-1, 7, now.Add(-10*time.Minute)); !reflect.DeepEqual(got, []int{12}) {
		t.Fatalf("got %v, want [12]", got)
	}
	// Exactly at the edge counts.
	if got := b.Since(-1, 7, now.Add(-20*time.Minute)); !reflect.DeepEqual(got, []int{10, 12}) {
		t.Fatalf("edge: got %v, want [10 12]", got)
	}
}

func TestRecordPrunesOlderThanWindow(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	b := newAt(time.Hour, &now)
	b.Record(-1, 7, 10, time.Time{})
	b.Record(-1, 9, 20, time.Time{})
	now = now.Add(61 * time.Minute)
	b.Record(-1, 7, 11, time.Time{})
	if got := b.Since(-1, 7, time.Time{}); !reflect.DeepEqual(got, []int{11}) {
		t.Fatalf("got %v, want the expired entry pruned", got)
	}
	// The sweep also forgot a sender who never posted again.
	b.mu.Lock()
	_, kept := b.m[key{-1, 9}]
	b.mu.Unlock()
	if kept {
		t.Fatal("idle sender not swept")
	}
}

func TestRecordCapsPerSender(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	b := newAt(time.Hour, &now)
	for i := 1; i <= MaxPerSender+5; i++ {
		b.Record(-1, 7, i, time.Time{})
	}
	got := b.Since(-1, 7, time.Time{})
	if len(got) != MaxPerSender || got[0] != 6 || got[len(got)-1] != MaxPerSender+5 {
		t.Fatalf("len=%d first=%d last=%d, want the newest %d", len(got), got[0], got[len(got)-1], MaxPerSender)
	}
}

func TestDisabledWindowRecordsNothing(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	b := newAt(0, &now)
	b.Record(-1, 7, 10, time.Time{})
	b.Record(-1, 0, 11, time.Time{})
	if got := b.Since(-1, 7, time.Time{}); len(got) != 0 {
		t.Fatalf("got %v, want nothing with a zero window", got)
	}
	b = newAt(time.Hour, &now)
	b.Record(-1, 0, 11, time.Time{})
	if got := b.Since(-1, 0, time.Time{}); len(got) != 0 {
		t.Fatalf("user 0 recorded: %v", got)
	}
}

// Between sweeps a sender's own expired entries are dropped on their next
// message, so an active sender's slice stays bounded by the window.
func TestRecordPrunesSendersOwnEntriesBetweenSweeps(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	now := t0
	b := newAt(time.Hour, &now)
	b.Record(-1, 7, 10, time.Time{}) // sweep at t0
	now = t0.Add(30 * time.Minute)
	b.Record(-1, 7, 11, time.Time{})
	now = t0.Add(70 * time.Minute)
	b.Record(-1, 7, 12, time.Time{}) // sweep at t0+70m drops 10
	now = t0.Add(100 * time.Minute)
	b.Record(-1, 7, 13, time.Time{}) // no sweep; 11 (t0+30m) is now expired
	b.mu.Lock()
	n := len(b.m[key{-1, 7}])
	b.mu.Unlock()
	if n != 2 {
		t.Fatalf("held %d entries, want 2 (12, 13)", n)
	}
}

// A late-processed backlog is judged by Telegram's send time: a message sent
// before the window is not kept, and order of arrival does not matter.
func TestRecordUsesSendTime(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	b := newAt(time.Hour, &now)
	b.Record(-1, 7, 20, now.Add(-10*time.Minute))
	b.Record(-1, 7, 10, now.Add(-2*time.Hour)) // sent before the window
	b.Record(-1, 7, 15, now.Add(-50*time.Minute))
	b.Record(-1, 7, 30, now.Add(time.Hour)) // clock skew: clamped to now
	if got := b.Since(-1, 7, now.Add(-time.Hour)); !reflect.DeepEqual(got, []int{15, 20, 30}) {
		t.Fatalf("got %v, want [15 20 30]", got)
	}
	if got := b.Since(-1, 7, now.Add(-30*time.Minute)); !reflect.DeepEqual(got, []int{20, 30}) {
		t.Fatalf("got %v, want [20 30]: 15 was sent 50m ago", got)
	}
	// 15 (sent at -50m) expires first although it arrived after 20.
	now = now.Add(15 * time.Minute)
	b.Record(-1, 7, 40, time.Time{})
	b.mu.Lock()
	n := len(b.m[key{-1, 7}])
	b.mu.Unlock()
	if n != 3 {
		t.Fatalf("held %d entries, want 3 (20, 30, 40)", n)
	}
	// A sender whose only message is already stale leaves no key behind.
	b.Record(-1, 9, 50, now.Add(-3*time.Hour))
	b.mu.Lock()
	_, kept := b.m[key{-1, 9}]
	b.mu.Unlock()
	if kept {
		t.Fatal("stale-only sender kept a key")
	}
}

// A send time ahead of our clock is clamped, so skew cannot keep an entry
// past the window.
func TestFutureSendTimeIsClamped(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	b := newAt(time.Hour, &now)
	b.Record(-1, 7, 30, now.Add(time.Hour))
	now = now.Add(61 * time.Minute)
	if got := b.Since(-1, 7, now.Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("got %v, want the skewed entry expired with the window", got)
	}
}
