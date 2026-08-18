package telegram

import (
	"sync"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestAlbumBufferGroupsParts(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]domain.Message
	var fire func()
	a := NewAlbumBuffer(700*time.Millisecond, func(parts []domain.Message) {
		mu.Lock()
		cp := append([]domain.Message(nil), parts...)
		flushed = append(flushed, cp)
		mu.Unlock()
	})
	a.afterFunc = func(_ time.Duration, fn func()) *time.Timer { fire = fn; return time.NewTimer(time.Hour) }

	standalone := a.Add(domain.Message{ChatID: -1, MessageID: 1})
	if !standalone {
		t.Fatal("message without media_group_id must be standalone")
	}
	if a.Add(domain.Message{ChatID: -1, MessageID: 10, MediaGroupID: "g"}) {
		t.Fatal("first album part must be buffered, not standalone")
	}
	if a.Add(domain.Message{ChatID: -1, MessageID: 11, MediaGroupID: "g"}) {
		t.Fatal("second album part must be buffered")
	}
	fire() // window elapses
	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 || len(flushed[0]) != 2 {
		t.Fatalf("expected one flush of 2 parts, got %v", flushed)
	}
	if flushed[0][0].MessageID != 10 || flushed[0][1].MessageID != 11 {
		t.Fatalf("parts out of order: %v", flushed[0])
	}
}

func TestAlbumBufferStopFlushesPendingParts(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]domain.Message
	a := NewAlbumBuffer(time.Hour, func(parts []domain.Message) {
		mu.Lock()
		cp := append([]domain.Message(nil), parts...)
		flushed = append(flushed, cp)
		mu.Unlock()
	})
	a.afterFunc = func(_ time.Duration, fn func()) *time.Timer { return time.NewTimer(time.Hour) } // never fires on its own

	if a.Add(domain.Message{ChatID: -1, MessageID: 20, MediaGroupID: "g"}) {
		t.Fatal("first album part must be buffered")
	}
	if a.Add(domain.Message{ChatID: -1, MessageID: 21, MediaGroupID: "g"}) {
		t.Fatal("second album part must be buffered")
	}

	a.Stop() // shutdown before the window elapses must not silently drop buffered parts

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 || len(flushed[0]) != 2 {
		t.Fatalf("expected Stop to flush the buffered group, got %v", flushed)
	}
	if flushed[0][0].MessageID != 20 || flushed[0][1].MessageID != 21 {
		t.Fatalf("parts out of order: %v", flushed[0])
	}
}
