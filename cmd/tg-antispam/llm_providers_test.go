package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/llm"
)

func TestConfiguredJEVProvider(t *testing.T) {
	t.Setenv("JEV_TOKEN", "test-env")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-env" {
			t.Error("env token not wired")
		}
		var body struct {
			Model     string
			State     map[string]string
			Questions map[string]struct{ Instructions string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "custom" || body.State["message"] != "Пр" || body.Questions["spam"].Instructions != "policy" {
			t.Errorf("settings not wired: %+v", body)
		}
		fmt.Fprint(w, `{"answers":{"spam":{"type":"noul","noul":0.7}}}`)
	}))
	defer srv.Close()
	c, err := config.Parse([]byte(fmt.Sprintf(`bot_token: test
admin_chat_id: -1
action: mute
chats:
  mode: auto
llm:
  enabled: true
  prompt: policy
  providers:
    - kind: jev
      model: custom
      threshold: 0.8
      max_symbols: 2
      api_base: %s
`, srv.URL)))
	if err != nil {
		t.Fatal(err)
	}
	providers := llmProviders(c.LLM)
	if len(providers) != 1 || providers[0].Name() != "jev" {
		t.Fatal("JEV not constructed")
	}
	result := (llm.Judge{Providers: providers, Policy: llm.PolicyAny}).Adjudicate(context.Background(), "Привет", "")
	if result.Spam || result.Failed != 0 {
		t.Fatalf("custom threshold not applied: %+v", result)
	}
}

func TestConfiguredProviderKinds(t *testing.T) {
	threshold, maxSymbols := .3, 6000
	providers := llmProviders(config.LLM{Providers: []config.LLMProvider{
		{Kind: "openai", APIKey: "test", Model: "m"},
		{Kind: "anthropic", APIKey: "test", Model: "m"},
		{Kind: "jev", APIKey: "test", Model: "m", Threshold: &threshold, MaxSymbols: &maxSymbols},
	}})
	if len(providers) != 3 {
		t.Fatalf("providers=%d", len(providers))
	}
	for i, want := range []string{"openai", "anthropic", "jev"} {
		if providers[i].Name() != want {
			t.Fatalf("provider %d=%s", i, providers[i].Name())
		}
	}
}
