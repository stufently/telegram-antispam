package config

import (
	"strings"
	"testing"
)

func shadowConfig(t *testing.T, extra string) *Config {
	t.Helper()
	c, err := Parse([]byte(baseValidYAML + "llm:\n  enabled: true\n  providers:\n    - kind: openai\n      api_key: fake\n      model: fake\n" + extra))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLLMProviderShadowPromptParse(t *testing.T) {
	c := shadowConfig(t, "      shadow: true\n      prompt: provider\n      prompt_overrides:\n        -123: chat\n    - kind: openai\n      api_key: fake\n      model: fake\n")
	p := c.LLM.Providers[0]
	if !p.Shadow || p.Prompt != "provider" || p.PromptOverrides[-123] != "chat" {
		t.Fatalf("provider=%+v", p)
	}
}

func TestLLMRequiresVotingProvider(t *testing.T) {
	c := shadowConfig(t, "")
	c.LLM.Providers[0].Shadow = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "voting") {
		t.Fatalf("err=%v", err)
	}
	c.LLM.Providers = append(c.LLM.Providers, LLMProvider{Kind: "openai", APIKey: "fake", Model: "fake"})
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.LLM.Providers[1].Shadow = true
	*c.LLM.Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLLMProviderPromptCoversChatOverrides(t *testing.T) {
	c := shadowConfig(t, "")
	c.LLM.PromptOverrides = map[int64]string{-123: "shared chat", -456: " "}
	p := &c.LLM.Providers[0]
	p.Prompt = "provider"
	p.PromptOverrides = map[int64]string{-456: "covered"}
	for _, blank := range []string{"", " \n"} {
		p.PromptOverrides[-123] = blank
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "llm.providers[0]") || !strings.Contains(err.Error(), "-123") {
			t.Fatalf("err=%v", err)
		}
	}
	p.PromptOverrides[-123] = "own chat"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Prompt = " "
	p.PromptOverrides = nil
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderPromptForPrecedence(t *testing.T) {
	l := LLM{Prompt: "global", PromptOverrides: map[int64]string{-1: "global chat"}, Providers: []LLMProvider{{Prompt: "provider", PromptOverrides: map[int64]string{-1: "provider chat"}}}}
	check := func(want string) {
		t.Helper()
		if got := l.ProviderPromptFor(0, -1); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
	check("provider chat")
	l.Providers[0].PromptOverrides[-1] = " \n"
	check("provider")
	l.Providers[0].Prompt = " "
	check("global chat")
	l.PromptOverrides[-1] = " "
	check("global")
	l.Prompt = ""
	check("")
}
