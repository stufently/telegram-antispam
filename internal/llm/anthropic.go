package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Anthropic classifies via the official Anthropic Messages API.
type Anthropic struct {
	APIKey  string
	Model   string       // e.g. "claude-3-5-haiku-latest"
	BaseURL string       // override for tests; defaults to the public endpoint
	Client  *http.Client // override for tests; defaults to http.DefaultClient
	// Prompt overrides the built-in system prompt; empty uses classifyPrompt.
	Prompt string
	// Temperature is sent only when non-nil (see OpenAI.Temperature).
	Temperature *float64
	// MaxTokens caps the reply. Unlike OpenAI, the Messages API REQUIRES
	// max_tokens, so an unset value falls back to defaultAnthropicMaxTokens
	// rather than being omitted.
	MaxTokens int
}

// defaultAnthropicMaxTokens is the fallback cap for the Messages API, which
// rejects a request without max_tokens. It is small because the expected
// answer is one word, but not so small that a model prefixing a space or a
// newline gets truncated to nothing.
const defaultAnthropicMaxTokens = 16

func (a Anthropic) Name() string { return "anthropic" }

func (a Anthropic) Classify(ctx context.Context, text, prompt string) (bool, error) {
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}
	payload := map[string]any{
		"model":      a.Model,
		"max_tokens": maxTokens,
		"system":     promptOr(prompt, a.Prompt),
		"messages": []map[string]string{
			{"role": "user", "content": text},
		},
	}
	if a.Temperature != nil {
		payload["temperature"] = *a.Temperature
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", a.APIKey)
	req.Header.Set("Anthropic-Version", "2023-06-01")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	for _, c := range out.Content {
		if c.Type == "text" {
			return classifyReply(c.Text), nil
		}
	}
	return false, fmt.Errorf("anthropic: no text content")
}

func (a Anthropic) httpClient() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return http.DefaultClient
}
