package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJEVRequestAndThreshold(t *testing.T) {
	for _, probability := range []float64{0, 0.2999, 0.3, 1} {
		t.Run(fmt.Sprint(probability), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					Model     string            `json:"model"`
					State     map[string]string `json:"state"`
					Questions map[string]struct {
						Type         string            `json:"type"`
						Instructions string            `json:"instructions"`
						Criteria     map[string]string `json:"criteria"`
					} `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				q := body.Questions["spam"]
				if body.Model != "test-model" || body.State["message"] != "Пр🙂" || len(body.State) != 1 || len(body.Questions) != 1 || q.Type != "noul" || q.Instructions != "chat policy" || q.Criteria["true"] == "" || q.Criteria["false"] == "" {
					t.Errorf("unexpected payload: %+v", body)
				}
				fmt.Fprintf(w, `{"answers":{"spam":{"type":"noul","noul":%g}}}`, probability)
			}))
			defer srv.Close()
			p := JEV{APIKey: "test-token", Model: "test-model", BaseURL: srv.URL + "/v1/systemone", Client: srv.Client(), Threshold: 0.3, MaxSymbols: 3, Prompt: "shared policy"}
			got, err := p.Classify(context.Background(), "Пр🙂ивет", "chat policy")
			if err != nil || got != (probability >= 0.3) {
				t.Fatalf("spam=%v err=%v", got, err)
			}
			if p.Name() != "jev" {
				t.Fatal(p.Name())
			}
		})
	}
}

func TestJEVInvalidResponseFailsOpenAndDoesNotLeak(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
	}{
		{"missing", `{}`, 200}, {"null answer", `{"answers":{"spam":null}}`, 200},
		{"missing probability", `{"answers":{"spam":{"type":"noul"}}}`, 200},
		{"null probability", `{"answers":{"spam":{"type":"noul","noul":null}}}`, 200},
		{"string probability", `{"answers":{"spam":{"type":"noul","noul":"secret-message"}}}`, 200},
		{"wrong type", `{"answers":{"spam":{"type":"secret-message","noul":1}}}`, 200},
		{"negative", `{"answers":{"spam":{"type":"noul","noul":-0.1}}}`, 200},
		{"above one", `{"answers":{"spam":{"type":"noul","noul":1.1}}}`, 200},
		{"malformed", `secret-message`, 200}, {"empty", "", 204},
		{"trailing", `{"answers":{"spam":{"type":"noul","noul":1}}} secret-message`, 200},
		{"oversized", strings.Repeat(" ", 65537), 200},
		{"unauthorized", "secret-message test-token", 401}, {"rate", "secret-message", 429}, {"server", "secret-message", 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			p := JEV{APIKey: "test-token", BaseURL: srv.URL, Threshold: .3, MaxSymbols: 6000}
			out := (Judge{Providers: []Provider{p}, Policy: PolicyAny}).Adjudicate(context.Background(), "secret-message", "")
			if out.Spam || out.Failed != 1 || out.Err == nil {
				t.Fatalf("out=%+v", out)
			}
			if strings.Contains(out.Err.Error(), "secret-message") || strings.Contains(out.Err.Error(), "test-token") {
				t.Fatalf("leaky error: %s", out.Err)
			}
		})
	}
}

func TestJEVRejectsInvalidSettingsBeforeRequest(t *testing.T) {
	for _, threshold := range []float64{0, -1, 1.01, math.NaN(), math.Inf(1)} {
		p := JEV{Threshold: threshold, MaxSymbols: 1}
		if spam, err := p.Classify(context.Background(), "x", ""); spam || err == nil {
			t.Fatalf("threshold %v: spam=%v err=%v", threshold, spam, err)
		}
	}
	for _, max := range []int{0, -1} {
		p := JEV{Threshold: .3, MaxSymbols: max}
		if spam, err := p.Classify(context.Background(), "x", ""); spam || err == nil {
			t.Fatalf("max %d: spam=%v err=%v", max, spam, err)
		}
	}
}

func TestJEVContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	p := JEV{APIKey: "test-token", BaseURL: srv.URL, Threshold: .3, MaxSymbols: 6000}
	spam, err := p.Classify(ctx, "x", "")
	if spam || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("spam=%v err=%v", spam, err)
	}
}

func TestJEVConcurrentPrompts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State     map[string]string
			Questions map[string]struct{ Instructions string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.State["message"] != body.Questions["spam"].Instructions {
			t.Error("cross-chat prompt mismatch")
		}
		fmt.Fprint(w, `{"answers":{"spam":{"type":"noul","noul":0}}}`)
	}))
	defer srv.Close()
	p := JEV{APIKey: "test-token", BaseURL: srv.URL, Threshold: .3, MaxSymbols: 6000, Prompt: "shared"}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			s := fmt.Sprint(i)
			if _, err := p.Classify(context.Background(), s, s); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestJEVInstructionFallbacks(t *testing.T) {
	for _, tc := range []struct{ name, shared, chat, want string }{
		{"built in", "", "", jevQuestion},
		{"shared", "shared policy", " ", "shared policy"},
		{"chat", "shared policy", "chat policy", "chat policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model     string
					Questions map[string]struct{ Instructions string }
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != "jev-1.13.0" || body.Questions["spam"].Instructions != tc.want {
					t.Errorf("unexpected request: %+v", body)
				}
				fmt.Fprint(w, `{"answers":{"spam":{"type":"noul","noul":0}}}`)
			}))
			defer srv.Close()
			p := JEV{BaseURL: srv.URL, Prompt: tc.shared, Threshold: .3, MaxSymbols: 6000}
			if spam, err := p.Classify(context.Background(), "x", tc.chat); spam || err != nil {
				t.Fatalf("spam=%v err=%v", spam, err)
			}
		})
	}
}

func TestJEVConsensusWithAnotherProvider(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		policy      Policy
		other, spam bool
		failed      int
	}{
		{"any JEV spam", `{"answers":{"spam":{"type":"noul","noul":1}}}`, PolicyAny, false, true, 0},
		{"all JEV ham", `{"answers":{"spam":{"type":"noul","noul":0}}}`, PolicyAll, true, false, 0},
		{"all JEV failure", `{}`, PolicyAll, true, false, 1},
		{"any JEV failure", `{}`, PolicyAny, true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			p := JEV{BaseURL: srv.URL, Threshold: .3, MaxSymbols: 6000}
			out := (Judge{Providers: []Provider{p, stub{spam: tc.other}}, Policy: tc.policy}).Adjudicate(context.Background(), "x", "")
			if out.Spam != tc.spam || out.Failed != tc.failed || out.Total != 2 {
				t.Fatalf("unexpected consensus: %+v", out)
			}
		})
	}
}
