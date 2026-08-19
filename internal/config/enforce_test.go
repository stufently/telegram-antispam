package config

import (
	"strings"
	"testing"
)

func TestDryRunForConfigWinsOverStoredValue(t *testing.T) {
	p := ChatsPolicy{Enforce: []int64{-100}, ForceDryRun: []int64{-200}}

	// enforce promotes a chat that registered in dry-run — the whole point:
	// start_in_dry_run only ever seeds a NEW row, so without this an observed
	// chat could be taken live only by editing SQLite.
	if p.DryRunFor(-100, true) {
		t.Fatal("chat in enforce must be live even though the stored row says dry-run")
	}
	// force_dry_run demotes a chat that registered live.
	if !p.DryRunFor(-200, false) {
		t.Fatal("chat in force_dry_run must be dry-run even though the stored row says live")
	}
	// unlisted chats keep whatever was stored, in both directions.
	if !p.DryRunFor(-300, true) || p.DryRunFor(-300, false) {
		t.Fatal("unlisted chat must keep its stored value")
	}
}

func TestDryRunForForceWinsOverEnforce(t *testing.T) {
	p := ChatsPolicy{Enforce: []int64{-100}, ForceDryRun: []int64{-100}}
	if !p.DryRunFor(-100, false) {
		t.Fatal("force_dry_run must win the conflict: the safe direction has the final word")
	}
}

func TestValidateRejectsEnforceOutsideAllowlist(t *testing.T) {
	// An enforce entry outside the allowlist never fires — the allowlist gate
	// drops the update first — so it must fail loudly rather than look
	// configured while moderating nothing.
	_, err := Parse([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: allowlist\n  allowlist: [-100]\n  enforce: [-200]\n"))
	if err == nil || !strings.Contains(err.Error(), "chats.enforce") {
		t.Fatalf("err = %v, want a chats.enforce/allowlist mismatch error", err)
	}

	// The same enforce entry is fine when it is inside the allowlist...
	if _, err := Parse([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: allowlist\n  allowlist: [-100]\n  enforce: [-100]\n")); err != nil {
		t.Fatalf("enforce inside allowlist rejected: %v", err)
	}
	// ...and in auto mode, where there is no allowlist to be inside of.
	if _, err := Parse([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n  enforce: [-200]\n")); err != nil {
		t.Fatalf("enforce in auto mode rejected: %v", err)
	}
}

func TestLLMAPIKeysComeFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-from-env")

	yaml := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n" +
		"llm:\n  enabled: true\n  providers:\n" +
		"    - kind: openai\n      model: m\n      api_key: in-file\n" +
		"    - kind: anthropic\n      model: m\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	// Env wins over the file: in a Kubernetes deploy the config file is a
	// ConfigMap in version control, so the key must be able to arrive
	// separately (from a Secret) exactly like BOT_TOKEN does.
	if got := c.LLM.Providers[0].APIKey; got != "sk-from-env" {
		t.Errorf("openai key = %q, want the env value", got)
	}
	if got := c.LLM.Providers[1].APIKey; got != "sk-ant-from-env" {
		t.Errorf("anthropic key = %q, want the env value", got)
	}
}

func TestLLMTuningParamsPreserveUnsetVersusZero(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n" +
		"llm:\n  enabled: true\n  providers:\n    - kind: openai\n      model: m\n      api_key: k\n"

	c, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.Temperature != nil {
		t.Errorf("temperature = %v, want nil (unset ⇒ not sent)", *c.LLM.Temperature)
	}
	if c.LLM.MaxTokens != 0 {
		t.Errorf("max_tokens = %d, want 0 (unset ⇒ not sent)", c.LLM.MaxTokens)
	}

	c, err = Parse([]byte(base + "  temperature: 0\n  max_tokens: 8\n  prompt: ru\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.Temperature == nil || *c.LLM.Temperature != 0 {
		t.Errorf("temperature = %v, want an explicit 0 distinct from unset", c.LLM.Temperature)
	}
	if c.LLM.MaxTokens != 8 || c.LLM.Prompt != "ru" {
		t.Errorf("max_tokens=%d prompt=%q", c.LLM.MaxTokens, c.LLM.Prompt)
	}
}

func TestOperatorsParsed(t *testing.T) {
	c, err := Parse([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n  operators: [7, 42]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Chats.Operators) != 2 || c.Chats.Operators[0] != 7 || c.Chats.Operators[1] != 42 {
		t.Fatalf("operators = %v, want [7 42]", c.Chats.Operators)
	}
}
