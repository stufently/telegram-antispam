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

const jevCriteriaConfig = jevConfig + `      criteria_spam: base spam
      criteria_ham: base ham
`

func TestJEVCriteriaParsedAndKnown(t *testing.T) {
	input := strings.Replace(jevCriteriaConfig, "llm:\n", "llm:\n  prompt_overrides:\n    -5: shared chat\n", 1) +
		"      prompt_overrides:\n        -7: provider chat\n        -5: provider shared chat\n      prompt: question\n" +
		"      criteria_overrides:\n        -7: {spam: s7, ham: h7}\n        -5: {spam: s5, ham: h5}\n"
	if err := UnknownKeys([]byte(input)); err != nil {
		t.Fatalf("criteria keys reported unknown: %v", err)
	}
	c, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	p := c.LLM.Providers[0]
	if p.CriteriaSpam != "base spam" || p.CriteriaHam != "base ham" || p.CriteriaOverrides[-7] != (JEVCriteria{Spam: "s7", Ham: "h7"}) || p.CriteriaOverrides[-5] != (JEVCriteria{Spam: "s5", Ham: "h5"}) {
		t.Fatalf("criteria not parsed: %+v", p)
	}
	// Omitted criteria stay valid: the legacy behaviour.
	if _, err := Parse([]byte(jevConfig + "      prompt_overrides:\n        -7: provider chat\n")); err != nil {
		t.Fatal(err)
	}
}

func TestJEVCriteriaValidation(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"spam only", jevConfig + "      criteria_spam: s\n", "set together"},
		{"ham only", jevConfig + "      criteria_ham: h\n", "set together"},
		{"blank ham", jevConfig + "      criteria_spam: s\n      criteria_ham: \"  \"\n", "set together"},
		{"overrides without pair", jevConfig + "      criteria_overrides:\n        -7: {spam: s, ham: h}\n", "requires criteria_spam"},
		{"override missing ham", jevCriteriaConfig + "      criteria_overrides:\n        -7: {spam: s}\n", "needs both"},
		{"override missing spam", jevCriteriaConfig + "      criteria_overrides:\n        -7: {spam: \" \", ham: h}\n", "needs both"},
		{"provider prompt override uncovered", jevCriteriaConfig + "      prompt_overrides:\n        -7: chat\n", "must cover chat -7"},
		{"shared prompt override uncovered", strings.Replace(jevCriteriaConfig, "llm:\n", "llm:\n  prompt_overrides:\n    -5: shared\n", 1), "must cover chat -5"},
		{"other kind", strings.ReplaceAll(jevConfig, "kind: jev", "kind: openai") + "      model: m\n      criteria_spam: s\n      criteria_ham: h\n", "require kind jev"},
		{"other kind ham only", strings.ReplaceAll(jevConfig, "kind: jev", "kind: openai") + "      model: m\n      criteria_ham: h\n", "require kind jev"},
		{"other kind overrides", strings.ReplaceAll(jevConfig, "kind: jev", "kind: anthropic") + "      model: m\n      criteria_overrides:\n        -7: {spam: s, ham: h}\n", "require kind jev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
