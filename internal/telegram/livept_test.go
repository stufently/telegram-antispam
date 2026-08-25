package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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

// sendAdminProbe runs one SendAdmin against a stub Telegram that records the
// reply_parameters of every sendMessage it receives. reply returns the raw
// JSON body for attempt n (1-based) so a test can make the first attempt fail.
func sendAdminProbe(t *testing.T, msg AdminMessage, reply func(attempt int) string) []string {
	t.Helper()
	var bodies []string
	var mu sync.Mutex
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/bot123:token/sendMessage" {
			t.Errorf("unexpected Telegram method path %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse form: %v", err)
		}
		mu.Lock()
		attempts++
		n := attempts
		bodies = append(bodies, r.FormValue("reply_parameters"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, reply(n))
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
	defer func() {
		cancel()
		<-done
	}()

	id, err := NewLivePort(b, disp, func(string) queue.Priority { return queue.PrioNormal }).
		SendAdmin(context.Background(), -100777, msg)
	if err != nil {
		t.Fatalf("SendAdmin: %v", err)
	}
	if id != 42 {
		t.Fatalf("card message id = %d, want 42", id)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), bodies...)
}

const sendMessageOK = `{"ok":true,"result":{"message_id":42,"date":1,"chat":{"id":-100777,"type":"supergroup"}}}`

// TestSendAdminThreadsCardOntoEvidence: the card must be a REPLY to the copied
// evidence, not merely the next message in the admin chat. Incidents from
// different source chats run in parallel, so admin-chat order can interleave
// evidence and cards; a reviewer pairing them by position would then judge one
// incident's evidence by another's verdict.
func TestSendAdminThreadsCardOntoEvidence(t *testing.T) {
	// An album: several evidence copies, one card. The first id anchors it.
	bodies := sendAdminProbe(t, AdminMessage{
		Text:           "verdict",
		CopyMessageIDs: []int{555, 556, 557},
	}, func(int) string { return sendMessageOK })
	if len(bodies) != 1 {
		t.Fatalf("sendMessage attempts = %d, want 1", len(bodies))
	}
	var rp models.ReplyParameters
	if err := json.Unmarshal([]byte(bodies[0]), &rp); err != nil {
		t.Fatalf("reply_parameters %q: %v", bodies[0], err)
	}
	if rp.MessageID != 555 {
		t.Fatalf("card replies to message %d, want the first evidence copy 555", rp.MessageID)
	}
	if !rp.AllowSendingWithoutReply {
		t.Fatal("allow_sending_without_reply must be set: the card is the only trace of an incident and must go out even if the evidence was deleted meanwhile")
	}
	if rp.ChatID != nil {
		t.Fatalf("reply_parameters.chat_id = %v, want unset: Bot API reserves it for a target in a DIFFERENT chat, and the copy is in this one", rp.ChatID)
	}
}

// TestSendAdminKeepsThreadAcrossA429Retry: a rate limit is the dispatcher's to
// retry, not this code's. The retried attempt must still be threaded — dropping
// the reply on a 429 would silently unpair the card from its evidence exactly
// when the admin chat is busiest, which is when interleaving happens.
func TestSendAdminKeepsThreadAcrossA429Retry(t *testing.T) {
	bodies := sendAdminProbe(t, AdminMessage{Text: "verdict", CopyMessageIDs: []int{555}},
		func(attempt int) string {
			if attempt == 1 {
				return `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`
			}
			return sendMessageOK
		})
	if len(bodies) != 2 {
		t.Fatalf("sendMessage attempts = %d, want 2 (429, then the dispatcher's retry)", len(bodies))
	}
	for i, body := range bodies {
		var rp models.ReplyParameters
		if err := json.Unmarshal([]byte(body), &rp); err != nil {
			t.Fatalf("attempt %d reply_parameters %q: %v", i+1, body, err)
		}
		if rp.MessageID != 555 {
			t.Fatalf("attempt %d replied to %d, want 555 on every attempt", i+1, rp.MessageID)
		}
	}
}

// TestSendAdminWithoutEvidenceSendsPlainCard: no copy means nothing to thread
// onto (machine.go's StateEvidenceFailed branch passes CopyMessageIDs: nil),
// and the card must still be sent — unthreaded, exactly as before.
func TestSendAdminWithoutEvidenceSendsPlainCard(t *testing.T) {
	bodies := sendAdminProbe(t, AdminMessage{Text: "evidence copy failed"},
		func(int) string { return sendMessageOK })
	if len(bodies) != 1 {
		t.Fatalf("sendMessage attempts = %d, want 1", len(bodies))
	}
	if bodies[0] != "" {
		t.Fatalf("reply_parameters = %q, want none when there is no evidence to reply to", bodies[0])
	}
}

// TestSendAdminFallsBackWhenReplyTargetGone: allow_sending_without_reply does
// not cover every case Telegram refuses over a missing reply target, and a
// refused card is a lost incident. So a refusal that names the reply target
// must be retried unthreaded rather than surfaced.
func TestSendAdminFallsBackWhenReplyTargetGone(t *testing.T) {
	bodies := sendAdminProbe(t, AdminMessage{Text: "verdict", CopyMessageIDs: []int{555}},
		func(attempt int) string {
			if attempt == 1 {
				return `{"ok":false,"error_code":400,"description":"Bad Request: message to be replied not found"}`
			}
			return sendMessageOK
		})
	if len(bodies) != 2 {
		t.Fatalf("sendMessage attempts = %d, want 2 (threaded, then plain)", len(bodies))
	}
	if bodies[0] == "" {
		t.Fatal("first attempt must carry reply_parameters")
	}
	if bodies[1] != "" {
		t.Fatalf("retry still carried reply_parameters %q; it must drop the thread", bodies[1])
	}
}

// TestReplyTargetGone pins which refusals drop the thread. A 429 or a revoked
// right must NOT: retrying those unthreaded would burn a second request and
// hide the real failure.
func TestReplyTargetGone(t *testing.T) {
	if replyTargetGone(nil) {
		t.Fatal("nil is not a refusal")
	}
	// A rate limit belongs to the dispatcher, and it is excluded by TYPE, not
	// by wording — so even one whose description quotes the reply target must
	// not be resent from here.
	if replyTargetGone(&tgbot.TooManyRequestsError{Message: "message to be replied not found", RetryAfter: 3}) {
		t.Fatal("a 429 must reach mapRetry and the dispatcher, never an immediate unthreaded resend")
	}
	for _, msg := range []string{
		"Bad Request: message to be replied not found",
		"Bad Request: reply message not found",
		"Bad Request: MESSAGE_ID_INVALID",
	} {
		if !replyTargetGone(errors.New(msg)) {
			t.Fatalf("%q must drop the thread and resend", msg)
		}
	}
	for _, msg := range []string{
		"Bad Request: chat not found",
		"Forbidden: bot was kicked from the supergroup chat",
		"Too Many Requests: retry after 5",
	} {
		if replyTargetGone(errors.New(msg)) {
			t.Fatalf("%q is not about the reply target and must surface", msg)
		}
	}
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
