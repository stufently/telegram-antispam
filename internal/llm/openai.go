package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAI classifies via the official OpenAI Chat Completions API.
type OpenAI struct {
	APIKey  string
	Model   string       // e.g. "gpt-4o-mini"
	BaseURL string       // override for tests; defaults to the public endpoint
	Client  *http.Client // override for tests; defaults to http.DefaultClient
	// Prompt overrides the built-in system prompt; empty uses classifyPrompt.
	Prompt string
	// Temperature is sent only when non-nil. Reasoning models reject the
	// parameter outright, so an always-present "temperature": 0 would make
	// this provider unusable with them — the field is opt-in for that reason.
	Temperature *float64
	// MaxTokens is sent only when > 0. Omitting it is the safe default: a
	// small cap is spent on hidden reasoning tokens by reasoning models,
	// which then return an empty answer that classifyReply reads as HAM —
	// a silently disabled spam check rather than a visible error.
	MaxTokens int
}

func (o OpenAI) Name() string { return "openai" }

func (o OpenAI) Classify(ctx context.Context, text, prompt string) (bool, error) {
	base := o.BaseURL
	if base == "" {
		base = "https://api.openai.com"
	}
	payload := map[string]any{
		"model": o.Model,
		"messages": []map[string]string{
			{"role": "system", "content": promptOr(prompt, o.Prompt)},
			{"role": "user", "content": text},
		},
	}
	if o.Temperature != nil {
		payload["temperature"] = *o.Temperature
	}
	if o.MaxTokens > 0 {
		// Reasoning models (o-series, gpt-5, gpt-6) reject max_tokens with a
		// 400, so a configured cap would fail every call, and fail-open turns
		// that into a disabled check. They take max_completion_tokens.
		if reasoningModel(o.Model) {
			payload["max_completion_tokens"] = o.MaxTokens
		} else {
			payload["max_tokens"] = o.MaxTokens
		}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)

	resp, err := o.httpClient().Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("openai: status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	if len(out.Choices) == 0 {
		return false, fmt.Errorf("openai: empty choices")
	}
	return classifyReply(out.Choices[0].Message.Content), nil
}

func (o OpenAI) httpClient() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return http.DefaultClient
}

// reasoningModel reports whether model is an OpenAI reasoning model, which
// takes max_completion_tokens instead of max_tokens (the same test as
// umputun/tg-spam #467).
func reasoningModel(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") ||
		strings.Contains(m, "gpt-5") || strings.Contains(m, "gpt-6")
}
