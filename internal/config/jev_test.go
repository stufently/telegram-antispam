package config

import (
	"fmt"
	"strings"
	"testing"
)

const jevConfig = `bot_token: test
admin_chat_id: -1
action: mute
chats:
  mode: auto
llm:
  enabled: true
  providers:
    - kind: jev
      api_key: yaml-token
`

func TestJEVConfigDefaultsAndEnvironment(t *testing.T) {
	t.Setenv("JEV_TOKEN", "env-token")
	c, err := Parse([]byte(jevConfig))
	if err != nil {
		t.Fatal(err)
	}
	p := c.LLM.Providers[0]
	if p.APIKey != "env-token" || p.Model != "jev-1.13.0" || p.Threshold == nil || *p.Threshold != .3 || p.MaxSymbols == nil || *p.MaxSymbols != 6000 {
		t.Fatalf("wrong JEV defaults (model=%s)", p.Model)
	}
	t.Setenv("JEV_TOKEN", "")
	c, err = Parse([]byte(jevConfig + "      threshold: 1\n      max_symbols: 1\n      model: custom\n      api_base: https://proxy.example/full/path\n"))
	if err != nil {
		t.Fatal(err)
	}
	p = c.LLM.Providers[0]
	if p.APIKey != "yaml-token" || p.Model != "custom" || *p.Threshold != 1 || *p.MaxSymbols != 1 || p.APIBase != "https://proxy.example/full/path" {
		t.Fatal("explicit settings not preserved")
	}
}

func TestJEVConfigRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"threshold", "0"}, {"threshold", "-0.1"}, {"threshold", "1.1"}, {"threshold", ".nan"}, {"threshold", ".inf"},
		{"max_symbols", "0"}, {"max_symbols", "-1"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			_, err := Parse([]byte(jevConfig + fmt.Sprintf("      %s: %s\n", tc.key, tc.value)))
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("want %s validation error, got %v", tc.key, err)
			}
		})
	}
	t.Setenv("JEV_TOKEN", "")
	if _, err := Parse([]byte(strings.ReplaceAll(jevConfig, "      api_key: yaml-token\n", ""))); err == nil {
		t.Fatal("missing token accepted")
	}
}

func TestJEVOptionsRejectedForOtherProviders(t *testing.T) {
	for _, key := range []string{"threshold: 0.3", "max_symbols: 6000"} {
		input := strings.ReplaceAll(jevConfig, "kind: jev", "kind: anthropic") + "      model: test\n      " + key + "\n"
		if _, err := Parse([]byte(input)); err == nil || !strings.Contains(err.Error(), "require kind jev") {
			t.Fatal("JEV setting silently ignored by anthropic")
		}
	}
}
