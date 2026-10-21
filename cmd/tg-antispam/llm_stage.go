package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/llm"
	"github.com/stufently/telegram-antispam/internal/ops"
)

const llmShadowMaxInFlight = 8

// llmStage keeps observations separate from the voting moderation path.
// Configuration and test hooks are fixed before the stage is used concurrently.
type llmStage struct {
	cfg                        config.LLM
	judge                      llm.Judge
	shadow                     *llm.Shadow
	voteIndices, shadowIndices []int
	reg                        *ops.Registry
	timeout                    time.Duration
	logf                       func(format string, args ...any)
	afterShadow                func()
}

// newLLMStage consumes validated config and providers in configuration order.
func newLLMStage(cfg config.LLM, provs []llm.Provider, reg *ops.Registry, timeout time.Duration) *llmStage {
	s := &llmStage{cfg: cfg, judge: llm.Judge{Policy: llm.Policy(cfg.Policy)}, reg: reg, timeout: timeout}
	for i, p := range provs {
		if cfg.Providers[i].Shadow {
			if s.shadow == nil {
				s.shadow = &llm.Shadow{Timeout: timeout, Max: llmShadowMaxInFlight}
			}
			s.shadow.Providers = append(s.shadow.Providers, p)
			s.shadowIndices = append(s.shadowIndices, i)
		} else {
			s.judge.Providers = append(s.judge.Providers, p)
			s.voteIndices = append(s.voteIndices, i)
		}
	}
	return s
}

func (s *llmStage) prompts(indices []int, chatID int64) []string {
	prompts := make([]string, len(indices))
	for i, index := range indices {
		prompts[i] = s.cfg.ProviderPromptFor(index, chatID)
	}
	return prompts
}

func voteLabel(v llm.Vote) string {
	if !v.OK {
		return "error"
	}
	return boolLabel(v.Spam)
}

func (s *llmStage) adjudicate(ctx context.Context, m domain.Message) bool {
	logf := s.logf
	if logf == nil {
		logf = log.Printf
	}
	text := llmMessageText(m)
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	out := s.judge.AdjudicateEach(cctx, text, s.prompts(s.voteIndices, m.ChatID))
	cancel()
	// Fail-open makes an expired key, exhausted quota or timeout look like a
	// clean HAM answer. Count failures separately, and label an adjudication
	// with no successful answers as "error", never "ham", so a dead paid
	// detector cannot hide behind healthy-looking HAM checks.
	// Shadow observations are intentionally excluded from these metrics.
	if out.Failed > 0 {
		logf("llm: %d/%d provider(s) failed: %v", out.Failed, out.Total, out.Err)
		s.reg.IncCounter("tg_antispam_llm_provider_failures_total", float64(out.Failed))
	}
	voter := boolLabel(out.Spam)
	if out.Failed >= out.Total {
		voter = "error"
	}
	// ONE sample per adjudication, not per provider: under "any", [spam, ham]
	// is one spam verdict for one message. Counting each provider here would
	// stop this metric from meaning "messages the LLM judged".
	s.reg.IncCounter("tg_antispam_llm_checks_total", 1, "result", voter)
	for _, v := range out.Votes {
		s.providerCheck(v.Provider, "vote", voteLabel(v))
	}
	if s.shadow != nil {
		// Use the work context, not the voting timeout canceled above.
		accepted := s.shadow.Observe(ctx, text, s.prompts(s.shadowIndices, m.ChatID), func(votes []llm.Vote) {
			for _, v := range votes {
				result := voteLabel(v)
				s.providerCheck(v.Provider, "shadow", result)
				s.reg.IncCounter("tg_antispam_llm_shadow_agreement_total", 1, "provider", v.Provider, "voter", voter, "shadow", result)
				score := "-"
				if v.Score != nil {
					score = fmt.Sprintf("%.3f", *v.Score)
				}
				logf("llm shadow: chat=%d msg=%d provider=%s voter=%s shadow=%s score=%s", m.ChatID, m.MessageID, v.Provider, voter, result, score)
			}
			if s.afterShadow != nil {
				s.afterShadow()
			}
		})
		if !accepted {
			for _, p := range s.shadow.Providers {
				s.providerCheck(p.Name(), "shadow", "dropped")
			}
		}
	}
	return out.Spam
}

func (s *llmStage) providerCheck(provider, role, result string) {
	s.reg.IncCounter("tg_antispam_llm_provider_checks_total", 1, "provider", provider, "role", role, "result", result)
}
