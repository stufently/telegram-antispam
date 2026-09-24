package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func formOf(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Errorf("parse form: %v", err)
	}
	out := map[string]string{}
	for k, v := range r.PostForm {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func assertEphemeralForm(t *testing.T, form map[string]string, userID int64) {
	t.Helper()
	if _, ok := form["receiver_user_id"]; ok {
		t.Fatalf("top-level receiver_user_id=%q", form["receiver_user_id"])
	}
	raw := form["ephemeral_message_parameters"]
	var params struct {
		ReceiverUserID int64 `json:"receiver_user_id"`
	}
	if err := json.Unmarshal([]byte(raw), &params); err != nil || params.ReceiverUserID != userID {
		t.Fatalf("ephemeral_message_parameters=%q err=%v user=%d", raw, err, params.ReceiverUserID)
	}
	if form["parse_mode"] != "" {
		t.Fatalf("parse_mode=%q", form["parse_mode"])
	}
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func TestSendEphemeralUsesEphemeralParameters(t *testing.T) {
	const userID int64 = 4242
	var mu sync.Mutex
	var paths []string
	var form map[string]string
	p, prios, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := formOf(t, r)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		form = f
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":{"message_id":10,"ephemeral_message_id":55,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
	})
	defer stop()
	id, err := p.SendEphemeral(context.Background(), -100, userID, "only you")
	mu.Lock()
	defer mu.Unlock()
	if err != nil || id != 55 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/sendMessage") {
		t.Fatalf("paths=%v", paths)
	}
	assertEphemeralForm(t, form, userID)
	if len(*prios) != 1 || (*prios)[0] != "SendEphemeral" {
		t.Fatalf("prio=%v", *prios)
	}
}

func TestSendEphemeralDeletesPublicFallback(t *testing.T) {
	const userID int64 = 7
	var mu sync.Mutex
	var paths []string
	var deleteIDs string
	var sendForm map[string]string
	deleteFails := false
	p, prios, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := formOf(t, r)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		fail := deleteFails
		if strings.HasSuffix(r.URL.Path, "/sendMessage") && sendForm == nil {
			sendForm = f
		}
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			writeJSON(w, `{"ok":true,"result":{"message_id":77,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
		case strings.HasSuffix(r.URL.Path, "/deleteMessages"):
			mu.Lock()
			deleteIDs = f["message_ids"]
			mu.Unlock()
			if fail {
				writeJSON(w, `{"ok":false,"error_code":400,"description":"Bad Request: message can't be deleted"}`)
				return
			}
			writeJSON(w, `{"ok":true,"result":true}`)
		default:
			t.Errorf("path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := p.SendEphemeral(ctx, -100, userID, "should have been private")
	mu.Lock()
	gotDelete, gotPaths, gotPrios, gotForm := deleteIDs, append([]string(nil), paths...), append([]string(nil), *prios...), sendForm
	mu.Unlock()
	if !errors.Is(err, ErrEphemeralNotHonored) || id != 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if !strings.Contains(gotDelete, "77") {
		t.Fatalf("delete ids=%q", gotDelete)
	}
	if len(gotPaths) != 2 || !strings.HasSuffix(gotPaths[0], "/sendMessage") || !strings.HasSuffix(gotPaths[1], "/deleteMessages") {
		t.Fatalf("paths=%v", gotPaths)
	}
	assertEphemeralForm(t, gotForm, userID)
	if len(gotPrios) != 2 || gotPrios[0] != "SendEphemeral" || gotPrios[1] != "DeleteMessages" {
		t.Fatalf("prio=%v", gotPrios)
	}
	mu.Lock()
	deleteFails = true
	mu.Unlock()
	_, err = p.SendEphemeral(ctx, -100, userID, "still public")
	if !errors.Is(err, ErrEphemeralNotHonored) || !strings.Contains(err.Error(), "can't be deleted") {
		t.Fatalf("wrapped delete err=%v", err)
	}
}

func TestSendWelcomeUsesEphemeralParameters(t *testing.T) {
	const userID int64 = 99
	var mu sync.Mutex
	var form map[string]string
	var paths []string
	p, prios, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := formOf(t, r)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		form = f
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":{"message_id":3,"ephemeral_message_id":8,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
	})
	defer stop()
	id, err := p.SendWelcome(context.Background(), -100, userID, "welcome, plain text")
	mu.Lock()
	defer mu.Unlock()
	if err != nil || id != 8 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/sendMessage") {
		t.Fatalf("paths=%v", paths)
	}
	assertEphemeralForm(t, form, userID)
	if len(*prios) != 1 || (*prios)[0] != "SendWelcome" {
		t.Fatalf("prio=%v", *prios)
	}
}
