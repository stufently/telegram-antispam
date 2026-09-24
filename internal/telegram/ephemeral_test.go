package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"golang.org/x/time/rate"

	"github.com/stufently/telegram-antispam/internal/queue"
)

func startLivePort(t *testing.T, handler http.HandlerFunc) (*LivePort, *[]string, func()) {
	t.Helper()
	var mu sync.Mutex
	var prios []string
	srv := httptest.NewServer(handler)
	b, err := tgbot.New("123:token", tgbot.WithSkipGetMe(), tgbot.WithServerURL(srv.URL))
	if err != nil {
		srv.Close()
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
	p := NewLivePort(b, disp, func(method string) queue.Priority {
		mu.Lock()
		prios = append(prios, method)
		mu.Unlock()
		return queue.PrioNormal
	})
	return p, &prios, func() {
		cancel()
		<-done
		srv.Close()
	}
}

func readForm(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Errorf("parse form: %v", err)
		return nil
	}
	out := map[string]string{}
	for k, v := range r.PostForm {
		if len(v) > 0 {
			out[k] = v[0]
		} else {
			out[k] = ""
		}
	}
	return out
}

func assertEphemeralForm(t *testing.T, form map[string]string, userID int64) {
	t.Helper()
	if _, ok := form["receiver_user_id"]; ok {
		t.Fatalf("top-level receiver_user_id is present: %v", form["receiver_user_id"])
	}
	raw, ok := form["ephemeral_message_parameters"]
	if !ok {
		t.Fatal("ephemeral_message_parameters is missing")
	}
	var params struct {
		ReceiverUserID int64 `json:"receiver_user_id"`
	}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("ephemeral_message_parameters %q: %v", raw, err)
	}
	if params.ReceiverUserID != userID {
		t.Fatalf("receiver_user_id = %d, want %d (body %s)", params.ReceiverUserID, userID, raw)
	}
	if form["parse_mode"] != "" {
		t.Fatalf("parse_mode = %q, welcome and ephemeral text are plain", form["parse_mode"])
	}
}

func TestSendEphemeralUsesEphemeralParameters(t *testing.T) {
	const userID int64 = 4242
	var mu sync.Mutex
	var paths []string
	var form map[string]string
	p, prios, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		form = readForm(t, r)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":10,"ephemeral_message_id":55,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
	})
	defer stop()

	id, err := p.SendEphemeral(context.Background(), -100, userID, "only you")
	if err != nil {
		t.Fatal(err)
	}
	if id != 55 {
		t.Fatalf("ephemeral id = %d, want 55", id)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/sendMessage") {
		t.Fatalf("paths = %v, want one sendMessage and no delete", paths)
	}
	assertEphemeralForm(t, form, userID)
	if len(*prios) != 1 || (*prios)[0] != "SendEphemeral" {
		t.Fatalf("priority methods = %v, want [SendEphemeral]", *prios)
	}
}

func TestSendEphemeralDeletesPublicFallback(t *testing.T) {
	const userID int64 = 7
	var mu sync.Mutex
	var paths []string
	var deleteIDs string
	var forms []map[string]string
	deleteFails := false
	p, prios, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := readForm(t, r)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		forms = append(forms, f)
		failDelete := deleteFails
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":77,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
		case strings.HasSuffix(r.URL.Path, "/deleteMessages"):
			mu.Lock()
			deleteIDs = f["message_ids"]
			mu.Unlock()
			if failDelete {
				_, _ = fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request: message can't be deleted"}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"ok":true,"result":true}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := p.SendEphemeral(ctx, -100, userID, "should have been private")
	if !errors.Is(err, ErrEphemeralNotHonored) {
		t.Fatalf("err = %v, want ErrEphemeralNotHonored", err)
	}
	if id != 0 {
		t.Fatalf("id = %d, want 0 when the send was public", id)
	}
	mu.Lock()
	gotDelete := deleteIDs
	gotPaths := append([]string(nil), paths...)
	gotPrios := append([]string(nil), *prios...)
	sendForm := forms[0]
	mu.Unlock()
	if !strings.Contains(gotDelete, "77") {
		t.Fatalf("delete message_ids = %q, want 77", gotDelete)
	}
	if len(gotPaths) != 2 || !strings.HasSuffix(gotPaths[0], "/sendMessage") || !strings.HasSuffix(gotPaths[1], "/deleteMessages") {
		t.Fatalf("paths = %v, want sendMessage then deleteMessages", gotPaths)
	}
	assertEphemeralForm(t, sendForm, userID)
	if len(gotPrios) != 2 || gotPrios[0] != "SendEphemeral" || gotPrios[1] != "DeleteMessages" {
		t.Fatalf("priority methods = %v, want SendEphemeral then DeleteMessages (delete goes through the dispatcher)", gotPrios)
	}

	mu.Lock()
	deleteFails = true
	paths = nil
	mu.Unlock()
	_, err = p.SendEphemeral(ctx, -100, userID, "still public")
	if !errors.Is(err, ErrEphemeralNotHonored) {
		t.Fatalf("delete failure err = %v, want it wrapped in ErrEphemeralNotHonored", err)
	}
	if !strings.Contains(err.Error(), "can't be deleted") {
		t.Fatalf("delete failure was dropped: %v", err)
	}
}

func TestSendWelcomeUsesEphemeralParameters(t *testing.T) {
	const userID int64 = 99
	var mu sync.Mutex
	var form map[string]string
	var paths []string
	p, prios, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := readForm(t, r)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		form = f
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":3,"ephemeral_message_id":8,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
	})
	defer stop()

	id, err := p.SendWelcome(context.Background(), -100, userID, "welcome, plain text")
	if err != nil {
		t.Fatal(err)
	}
	if id != 8 {
		t.Fatalf("ephemeral id = %d, want 8", id)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/sendMessage") {
		t.Fatalf("paths = %v, want one sendMessage", paths)
	}
	assertEphemeralForm(t, form, userID)
	if len(*prios) != 1 || (*prios)[0] != "SendWelcome" {
		t.Fatalf("priority methods = %v, want [SendWelcome]", *prios)
	}
}
