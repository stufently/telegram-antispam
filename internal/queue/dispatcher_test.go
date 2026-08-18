package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestDispatcherRunsAndRetriesOn429(t *testing.T) {
	d := NewDispatcher(rate.NewLimiter(rate.Inf, 1), func(int64) *rate.Limiter {
		return rate.NewLimiter(rate.Inf, 1)
	})
	// make sleep instant so the retry doesn't stall the test
	d.sleep = func(context.Context, time.Duration) {}

	var mu sync.Mutex
	attempts := 0
	done := make(chan struct{})
	d.Submit(-100123, Job{Priority: PrioHigh, Run: func(context.Context) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			return RetryAfter{Seconds: 1} // first attempt 429s
		}
		close(done)
		return nil
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not complete after retry")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2 (one 429 + one success)", attempts)
	}
}
