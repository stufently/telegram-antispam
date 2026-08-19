package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stub struct {
	name string
	spam bool
	err  error
}

func (s stub) Name() string { return s.name }
func (s stub) Classify(context.Context, string) (bool, error) {
	return s.spam, s.err
}

func TestAdjudicateConsensus(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name   string
		policy Policy
		provs  []Provider
		want   bool
	}{
		{"no providers fail-open", PolicyAny, nil, false},
		{"any: one spam vote", PolicyAny, []Provider{stub{spam: false}, stub{spam: true}}, true},
		{"any: no spam vote", PolicyAny, []Provider{stub{spam: false}, stub{spam: false}}, false},
		{"any: error + spam still spam", PolicyAny, []Provider{stub{err: boom}, stub{spam: true}}, true},
		{"any: only errors fail-open", PolicyAny, []Provider{stub{err: boom}, stub{err: boom}}, false},
		{"all: unanimous spam", PolicyAll, []Provider{stub{spam: true}, stub{spam: true}}, true},
		{"all: one ham breaks", PolicyAll, []Provider{stub{spam: true}, stub{spam: false}}, false},
		{"all: one error breaks unanimity", PolicyAll, []Provider{stub{spam: true}, stub{err: boom}}, false},
		{"single provider spam", PolicyAny, []Provider{stub{spam: true}}, true},
		{"single provider ham", PolicyAll, []Provider{stub{spam: false}}, false},
		{"unknown policy defaults to all", Policy("weird"), []Provider{stub{spam: true}, stub{spam: false}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge{Providers: tc.provs, Policy: tc.policy}.Adjudicate(context.Background(), "hi")
			if got != tc.want {
				t.Fatalf("Adjudicate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdjudicateContextCancelledFailOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A provider that would say spam, but ctx is already done.
	got := Judge{Providers: []Provider{blockingStub{}}, Policy: PolicyAny}.Adjudicate(ctx, "x")
	if got {
		t.Fatal("cancelled ctx must fail-open to not-spam")
	}
}

// blockingStub never returns until its Classify is abandoned by the ctx select.
type blockingStub struct{}

func (blockingStub) Name() string { return "block" }
func (blockingStub) Classify(ctx context.Context, _ string) (bool, error) {
	<-ctx.Done()
	return true, ctx.Err()
}

func TestClassifyReply(t *testing.T) {
	spam := []string{"SPAM", "spam", " Spam.", "SPAM — obvious ad", "*SPAM*"}
	ham := []string{"HAM", "ham", "not spam", "", "This looks fine", "SPAMMY? no"}
	for _, s := range spam {
		if !classifyReply(s) {
			t.Errorf("classifyReply(%q) = false, want true", s)
		}
	}
	for _, s := range ham {
		if classifyReply(s) {
			t.Errorf("classifyReply(%q) = true, want false", s)
		}
	}
}

func TestOpenAIClassify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth header = %q", got)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"SPAM"}}]}`))
	}))
	defer srv.Close()
	p := OpenAI{APIKey: "sk-test", Model: "gpt-4o-mini", BaseURL: srv.URL, Client: srv.Client()}
	spam, err := p.Classify(context.Background(), "buy now")
	if err != nil || !spam {
		t.Fatalf("spam=%v err=%v", spam, err)
	}
}

func TestOpenAIClassifyErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate"}`))
	}))
	defer srv.Close()
	p := OpenAI{APIKey: "k", Model: "m", BaseURL: srv.URL, Client: srv.Client()}
	if _, err := p.Classify(context.Background(), "x"); err == nil {
		t.Fatal("expected error on 429")
	}
}

func TestAnthropicClassify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "ak" {
			t.Errorf("api key header = %q", got)
		}
		if got := r.Header.Get("Anthropic-Version"); got == "" {
			t.Error("missing anthropic-version header")
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"HAM"}]}`))
	}))
	defer srv.Close()
	p := Anthropic{APIKey: "ak", Model: "claude-3-5-haiku-latest", BaseURL: srv.URL, Client: srv.Client()}
	spam, err := p.Classify(context.Background(), "hello friends")
	if err != nil || spam {
		t.Fatalf("spam=%v err=%v", spam, err)
	}
}
