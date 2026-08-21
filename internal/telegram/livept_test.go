package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"golang.org/x/time/rate"

	"github.com/stufently/telegram-antispam/internal/queue"
)

func TestBatchIDs(t *testing.T) {
	ids := make([]int, 250)
	for i := range ids {
		ids[i] = i + 1
	}
	batches := batchIDs(ids, 100)
	if len(batches) != 3 || len(batches[0]) != 100 || len(batches[2]) != 50 {
		t.Fatalf("batches: %d sizes %d/%d/%d", len(batches), len(batches[0]), len(batches[1]), len(batches[2]))
	}
}

func TestMeUsesDispatcherRetries429AndCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/bot123:token/getMe" {
			t.Errorf("unexpected Telegram method path %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"Bot","username":"test_bot"}}`)
	}))
	defer srv.Close()

	b, err := tgbot.New("123:token", tgbot.WithSkipGetMe(), tgbot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	disp := queue.NewDispatcher(rate.NewLimiter(rate.Inf, 1), func(int64) *rate.Limiter {
		return rate.NewLimiter(rate.Inf, 1)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		disp.Run(ctx)
		close(done)
	}()

	var priorityMethod string
	p := NewLivePort(b, disp, func(method string) queue.Priority {
		priorityMethod = method
		return queue.PrioNormal
	})
	id, err := p.me(context.Background(), -100)
	if err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	if id != 123 {
		t.Fatalf("me id = %d, want 123", id)
	}
	if priorityMethod != "GetMe" {
		t.Fatalf("priority method = %q, want GetMe", priorityMethod)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GetMe calls = %d, want 2 after one 429 retry", got)
	}

	if _, err := p.me(context.Background(), -100); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("cached GetMe made another HTTP call; calls=%d", got)
	}
	cancel()
	<-done
}

func TestSubmitSyncCancelsAttemptWithCaller(t *testing.T) {
	disp := queue.NewDispatcher(rate.NewLimiter(rate.Inf, 1), func(int64) *rate.Limiter { return nil })
	dispatchCtx, stopDispatcher := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		disp.Run(dispatchCtx)
		close(done)
	}()

	callerCtx, cancelCaller := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := submitSync(callerCtx, disp, 1, queue.PrioNormal, func(ctx context.Context) (struct{}, error) {
			close(started)
			<-ctx.Done()
			return struct{}{}, ctx.Err()
		})
		result <- err
	}()
	<-started
	cancelCaller()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected caller cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("Telegram attempt did not observe caller cancellation")
	}
	stopDispatcher()
	<-done
}

// TestIgnoreAlreadyGone: deleting a message that is already gone is the goal
// state, not a failure — tg-spam runs beside this bot on the same chats and
// routinely gets there first. But a revoked right must still surface.
func TestIgnoreAlreadyGone(t *testing.T) {
	if err := ignoreAlreadyGone(nil); err != nil {
		t.Fatalf("nil must stay nil, got %v", err)
	}
	if err := ignoreAlreadyGone(errors.New("Bad Request: message to delete not found")); err != nil {
		t.Fatalf("already-deleted message must not be an error, got %v", err)
	}
	for _, msg := range []string{
		"Bad Request: message can't be deleted",
		"Forbidden: bot is not a member of the supergroup chat",
	} {
		if err := ignoreAlreadyGone(errors.New(msg)); err == nil {
			t.Fatalf("%q must stay an error: it means the bot lost a right", msg)
		}
	}
}
