package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/llm"
	"github.com/stufently/telegram-antispam/internal/ops"
)

type stageFake struct {
	name   string
	spam   bool
	err    error
	prompt chan string
	gate   <-chan struct{}
}

func (p stageFake) Name() string { return p.name }
func (p stageFake) Classify(ctx context.Context, text, prompt string) (bool, error) {
	if p.prompt != nil {
		p.prompt <- prompt
	}
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return p.spam, p.err
}

type stageScore struct{ stageFake }

func (p stageScore) Score(context.Context, string, string) (float64, error) { return .42, nil }
func (p stageScore) Cutoff() float64                                        { return .42 }

type stageLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *stageLog) printf(f string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, f+"\n", args...)
}
func (l *stageLog) text() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }
func stageWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("shadow did not report")
	}
}
func stageMetrics(reg *ops.Registry) string { var b bytes.Buffer; reg.Write(&b); return b.String() }
func requireMetric(t *testing.T, reg *ops.Registry, line string) {
	t.Helper()
	if !strings.Contains(stageMetrics(reg), line+"\n") {
		t.Fatalf("missing %s in\n%s", line, stageMetrics(reg))
	}
}
func stageSetup(vote, shadow llm.Provider) (*llmStage, *ops.Registry, chan struct{}, *stageLog) {
	cfg := config.LLM{Policy: "any", Providers: []config.LLMProvider{{}, {Shadow: true}}}
	reg := ops.NewRegistry()
	done := make(chan struct{}, 32)
	logs := &stageLog{}
	s := newLLMStage(cfg, []llm.Provider{vote, shadow}, reg, time.Second)
	s.logf = logs.printf
	s.afterShadow = func() { done <- struct{}{} }
	return s, reg, done, logs
}

func TestLLMStageShadowNeverVotes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		vote, shadow bool
		err          error
	}{
		{"shadow spam", false, true, nil}, {"shadow ham", true, false, nil}, {"shadow error", false, true, errors.New("private failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, reg, done, _ := stageSetup(stageFake{name: "voter", spam: tc.vote}, stageFake{name: "observer", spam: tc.shadow, err: tc.err})
			if got := s.adjudicate(context.Background(), domain.Message{Text: "private text"}); got != tc.vote {
				t.Fatalf("got %v", got)
			}
			stageWait(t, done)
			requireMetric(t, reg, fmt.Sprintf(`tg_antispam_llm_checks_total{result="%s"} 1`, boolLabel(tc.vote)))
			if strings.Contains(stageMetrics(reg), "tg_antispam_llm_provider_failures_total") {
				t.Fatal(stageMetrics(reg))
			}
		})
	}
	// A blocked shadow must not hold up the synchronous moderation result.
	gate := make(chan struct{})
	defer close(gate)
	s, _, done, _ := stageSetup(stageFake{name: "voter", spam: true}, stageFake{name: "observer", gate: gate})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan bool, 1)
	go func() { returned <- s.adjudicate(ctx, domain.Message{}) }()
	select {
	case spam := <-returned:
		if !spam {
			t.Fatal("lost voting verdict")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("shadow blocked moderation")
	}
	cancel()
	stageWait(t, done)
}

func TestLLMStageMetrics(t *testing.T) {
	for _, tc := range []struct {
		voter, shadow string
		v, s          stageFake
	}{
		{"spam", "ham", stageFake{name: "v", spam: true}, stageFake{name: "s"}},
		{"ham", "spam", stageFake{name: "v"}, stageFake{name: "s", spam: true}},
		{"error", "spam", stageFake{name: "v", err: errors.New("v failed")}, stageFake{name: "s", spam: true}},
		{"ham", "error", stageFake{name: "v"}, stageFake{name: "s", err: errors.New("s failed")}},
	} {
		t.Run(tc.voter+tc.shadow, func(t *testing.T) {
			s, reg, done, _ := stageSetup(tc.v, tc.s)
			s.adjudicate(context.Background(), domain.Message{})
			stageWait(t, done)
			requireMetric(t, reg, fmt.Sprintf(`tg_antispam_llm_provider_checks_total{provider="v",result="%s",role="vote"} 1`, tc.voter))
			requireMetric(t, reg, fmt.Sprintf(`tg_antispam_llm_provider_checks_total{provider="s",result="%s",role="shadow"} 1`, tc.shadow))
			requireMetric(t, reg, fmt.Sprintf(`tg_antispam_llm_shadow_agreement_total{provider="s",shadow="%s",voter="%s"} 1`, tc.shadow, tc.voter))
			requireMetric(t, reg, fmt.Sprintf(`tg_antispam_llm_checks_total{result="%s"} 1`, tc.voter))
			if tc.voter == "error" {
				requireMetric(t, reg, "tg_antispam_llm_provider_failures_total 1")
			}
		})
	}
	gate := make(chan struct{})
	s, reg, done, _ := stageSetup(stageFake{name: "v"}, stageFake{name: "s", gate: gate})
	for range llmShadowMaxInFlight + 1 {
		s.adjudicate(context.Background(), domain.Message{})
	}
	requireMetric(t, reg, `tg_antispam_llm_provider_checks_total{provider="s",result="dropped",role="shadow"} 1`)
	close(gate)
	for range llmShadowMaxInFlight {
		stageWait(t, done)
	}
	requireMetric(t, reg, fmt.Sprintf(`tg_antispam_llm_checks_total{result="ham"} %d`, llmShadowMaxInFlight+1))
	requireMetric(t, reg, fmt.Sprintf(`tg_antispam_llm_shadow_agreement_total{provider="s",shadow="ham",voter="ham"} %d`, llmShadowMaxInFlight))
	// Partial voting failure preserves the consensus result, not error.
	cfg := config.LLM{Policy: "any", Providers: []config.LLMProvider{{}, {}}}
	reg = ops.NewRegistry()
	s = newLLMStage(cfg, []llm.Provider{stageFake{name: "good", spam: true}, stageFake{name: "bad", err: errors.New("bad")}}, reg, time.Second)
	s.logf = func(string, ...any) {}
	if s.shadow != nil || !s.adjudicate(context.Background(), domain.Message{}) {
		t.Fatal("wrong voting stage")
	}
	requireMetric(t, reg, `tg_antispam_llm_checks_total{result="spam"} 1`)
	requireMetric(t, reg, "tg_antispam_llm_provider_failures_total 1")
}

func TestLLMStageShadowLog(t *testing.T) {
	for _, tc := range []struct {
		provider      llm.Provider
		result, score string
	}{
		{stageScore{stageFake{name: "scored"}}, "spam", "0.420"},
		{stageFake{name: "plain"}, "ham", "-"},
		{stageFake{name: "failed", err: errors.New("private-error")}, "error", "-"},
	} {
		t.Run(tc.provider.Name(), func(t *testing.T) {
			s, _, done, logs := stageSetup(stageFake{name: "v"}, tc.provider)
			s.adjudicate(context.Background(), domain.Message{ChatID: -123, MessageID: 456, Text: "private-text"})
			stageWait(t, done)
			want := fmt.Sprintf("llm shadow: chat=-123 msg=456 provider=%s voter=ham shadow=%s score=%s\n", tc.provider.Name(), tc.result, tc.score)
			if got := logs.text(); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}
}

func TestLLMStagePerProviderPrompt(t *testing.T) {
	a, b := make(chan string, 1), make(chan string, 1)
	// Shadow first ensures the partition preserves original configuration indices.
	cfg := config.LLM{Policy: "any", Prompt: "global", PromptOverrides: map[int64]string{-1: "global chat"}, Providers: []config.LLMProvider{
		{Shadow: true, Prompt: "shadow", PromptOverrides: map[int64]string{-1: "shadow chat"}},
		{Prompt: "vote", PromptOverrides: map[int64]string{-1: "vote chat"}},
	}}
	s := newLLMStage(cfg, []llm.Provider{stageFake{name: "s", prompt: a}, stageFake{name: "v", prompt: b}}, ops.NewRegistry(), time.Second)
	done := make(chan struct{}, 1)
	s.afterShadow = func() { done <- struct{}{} }
	s.logf = func(string, ...any) {}
	s.adjudicate(context.Background(), domain.Message{ChatID: -1})
	stageWait(t, done)
	if got := <-a; got != "shadow chat" {
		t.Fatal(got)
	}
	if got := <-b; got != "vote chat" {
		t.Fatal(got)
	}
}
