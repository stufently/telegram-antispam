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
}

func (a Anthropic) Name() string { return "anthropic" }

func (a Anthropic) Classify(ctx context.Context, text string) (bool, error) {
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	body, _ := json.Marshal(map[string]any{
		"model":      a.Model,
		"max_tokens": 5,
		"system":     classifyPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": text},
		},
	})
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
