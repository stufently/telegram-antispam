package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type stub struct {
	name string
	spam bool
	err  error
}

func (s stub) Name() string { return s.name }
func (s stub) Classify(context.Context, string, string) (bool, error) {
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
			got := Judge{Providers: tc.provs, Policy: tc.policy}.Adjudicate(context.Background(), "hi", "")
			if got.Spam != tc.want {
				t.Fatalf("Adjudicate = %v, want %v", got.Spam, tc.want)
			}
		})
	}
}

func TestAdjudicateContextCancelledFailOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A provider that would say spam, but ctx is already done.
	got := Judge{Providers: []Provider{blockingStub{}}, Policy: PolicyAny}.Adjudicate(ctx, "x", "")
	if got.Spam {
		t.Fatal("cancelled ctx must fail-open to not-spam")
	}
	// The caller must be able to tell this apart from a model answering HAM:
	// fail-open makes an outage and a clean verdict the same boolean.
	if got.Failed != 1 || got.Err == nil {
		t.Fatalf("cancelled ctx: Failed=%d Err=%v, want 1 failure with an error", got.Failed, got.Err)
	}
}

// blockingStub never returns until its Classify is abandoned by the ctx select.
type blockingStub struct{}

func (blockingStub) Name() string { return "block" }
func (blockingStub) Classify(ctx context.Context, _, _ string) (bool, error) {
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

func TestOpenAIClassifyOmitsTuningParamsByDefault(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth header = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"SPAM"}}]}`))
	}))
	defer srv.Close()
	p := OpenAI{APIKey: "sk-test", Model: "gpt-4o-mini", BaseURL: srv.URL, Client: srv.Client()}
	spam, err := p.Classify(context.Background(), "buy now", "")
	if err != nil || !spam {
		t.Fatalf("spam=%v err=%v", spam, err)
	}
	// Neither key may be present unless the operator asked for it: reasoning
	// models reject an explicit temperature outright, and spend a small
	// max_tokens on hidden reasoning, returning an empty answer that reads
	// as HAM — i.e. a silently disabled check.
	if _, ok := body["temperature"]; ok {
		t.Errorf("temperature sent by default: %v", body["temperature"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Errorf("max_tokens sent by default: %v", body["max_tokens"])
	}
}

func TestOpenAIClassifySendsConfiguredPromptAndParams(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"HAM"}}]}`))
	}))
	defer srv.Close()
	temp := 0.0
	p := OpenAI{
		APIKey: "sk-test", Model: "m", BaseURL: srv.URL, Client: srv.Client(),
		Prompt: "ru prompt", Temperature: &temp, MaxTokens: 8,
	}
	if _, err := p.Classify(context.Background(), "hi", ""); err != nil {
		t.Fatal(err)
	}
	if v, ok := body["temperature"].(float64); !ok || v != 0 {
		t.Errorf("temperature = %v (ok=%v), want explicit 0", body["temperature"], ok)
	}
	if v, ok := body["max_tokens"].(float64); !ok || v != 8 {
		t.Errorf("max_tokens = %v, want 8", body["max_tokens"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages sent")
	}
	first, _ := msgs[0].(map[string]any)
	if first["content"] != "ru prompt" {
		t.Errorf("system prompt = %v, want the configured one", first["content"])
	}
}

func TestOpenAIClassifyErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate"}`))
	}))
	defer srv.Close()
	p := OpenAI{APIKey: "k", Model: "m", BaseURL: srv.URL, Client: srv.Client()}
	if _, err := p.Classify(context.Background(), "x", ""); err == nil {
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
	spam, err := p.Classify(context.Background(), "hello friends", "")
	if err != nil || spam {
		t.Fatalf("spam=%v err=%v", spam, err)
	}
}

// promptCapture records the prompt a provider was called with.
type promptCapture struct{ got string }

func (p *promptCapture) Name() string { return "capture" }
func (p *promptCapture) Classify(_ context.Context, _, prompt string) (bool, error) {
	p.got = prompt
	return false, nil
}

func TestPerCallPromptBeatsProviderAndBuiltIn(t *testing.T) {
	cap := &promptCapture{}
	Judge{Providers: []Provider{cap}, Policy: PolicyAny}.Adjudicate(context.Background(), "текст", "промпт чата")
	if cap.got != "промпт чата" {
		t.Fatalf("provider got prompt %q, want the per-call one", cap.got)
	}
}

func TestPromptOrFallbackOrder(t *testing.T) {
	if got := promptOr("  ", "provider"); got != "provider" {
		t.Errorf("blank per-call prompt must fall through to the provider one, got %q", got)
	}
	if got := promptOr("", ""); got != classifyPrompt {
		t.Errorf("with nothing configured the built-in prompt must be used, got %q", got)
	}
	if got := promptOr("chat", "provider"); got != "chat" {
		t.Errorf("per-call prompt must win, got %q", got)
	}
}

// TestAdjudicateReportsProviderFailure pins the distinction the metric
// depends on: a provider that errors must be counted as a failure, not
// silently folded into the fail-open "not spam" answer.
func TestAdjudicateReportsProviderFailure(t *testing.T) {
	got := Judge{Providers: []Provider{stub{name: "boom", err: errors.New("quota exhausted")}}, Policy: PolicyAny}.Adjudicate(context.Background(), "x", "")
	if got.Spam {
		t.Fatal("an errored provider must not produce a spam verdict")
	}
	if got.Failed != 1 || got.Total != 1 || got.Err == nil {
		t.Fatalf("Failed=%d Total=%d Err=%v, want one reported failure", got.Failed, got.Total, got.Err)
	}
}

// slowStub answers only when ctx dies, standing in for a provider that is
// still thinking when the deadline arrives.
type slowStub struct{}

func (slowStub) Name() string { return "slow" }
func (slowStub) Classify(ctx context.Context, _, _ string) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

// TestAdjudicateKeepsAnswersArrivedBeforeTheDeadline: under "any", a
// provider that already answered spam decides the verdict — a second,
// slower provider timing out must not retract it. And an error among the
// answers must survive alongside the timed-out one in Failed, otherwise the
// caller counts an outage as a clean ham check.
func TestAdjudicateKeepsAnswersArrivedBeforeTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	j := Judge{Providers: []Provider{stub{name: "fast", spam: true}, slowStub{}}, Policy: PolicyAny}
	got := j.Adjudicate(ctx, "куплю usdt", "")

	if !got.Spam {
		t.Fatal("a spam answer that arrived in time must stand despite the slow provider")
	}
	if got.Failed != 1 || got.Total != 2 {
		t.Fatalf("Failed=%d Total=%d, want exactly the slow provider counted as failed", got.Failed, got.Total)
	}
}

// TestAdjudicateCountsBothErroredAndTimedOutProviders pins the accounting
// the metric depends on: a provider that errored and a provider that never
// answered are two failures, not one.
func TestAdjudicateCountsBothErroredAndTimedOutProviders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	j := Judge{Providers: []Provider{stub{name: "boom", err: errors.New("quota")}, slowStub{}}, Policy: PolicyAny}
	got := j.Adjudicate(ctx, "x", "")

	if got.Failed != 2 {
		t.Fatalf("Failed = %d, want 2 (one errored, one timed out)", got.Failed)
	}
	if got.Spam {
		t.Fatal("two failures must not produce a spam verdict")
	}
}
