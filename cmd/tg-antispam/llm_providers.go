package main

import (
	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/llm"
)

// llmProviders consumes validated, defaulted configuration.
func llmProviders(cfg config.LLM) []llm.Provider {
	provs := make([]llm.Provider, 0, len(cfg.Providers))
	for _, pc := range cfg.Providers {
		switch pc.Kind {
		case "openai":
			provs = append(provs, llm.OpenAI{
				APIKey:          pc.APIKey,
				Model:           pc.Model,
				BaseURL:         pc.APIBase,
				Prompt:          cfg.Prompt,
				Temperature:     cfg.Temperature,
				MaxTokens:       cfg.MaxTokens,
				ReasoningEffort: cfg.ReasoningEffort,
			})
		case "anthropic":
			provs = append(provs, llm.Anthropic{
				APIKey:      pc.APIKey,
				Model:       pc.Model,
				BaseURL:     pc.APIBase,
				Prompt:      cfg.Prompt,
				Temperature: cfg.Temperature,
				MaxTokens:   cfg.MaxTokens,
			})
		case "jev":
			provs = append(provs, llm.JEV{
				APIKey: pc.APIKey, Model: pc.Model, BaseURL: pc.APIBase,
				Prompt: cfg.Prompt, Threshold: *pc.Threshold, MaxSymbols: *pc.MaxSymbols,
				Criteria:          llm.JEVCriteria{Spam: pc.CriteriaSpam, Ham: pc.CriteriaHam},
				CriteriaOverrides: jevCriteriaOverrides(pc.CriteriaOverrides),
			})
		}
	}
	return provs
}

func jevCriteriaOverrides(in map[int64]config.JEVCriteria) map[int64]llm.JEVCriteria {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int64]llm.JEVCriteria, len(in))
	for chatID, c := range in {
		out[chatID] = llm.JEVCriteria{Spam: c.Spam, Ham: c.Ham}
	}
	return out
}
