package config

import (
	"testing"
	"time"
)

func TestLoadValid(t *testing.T) {
	c, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminChatID != -1009999 || c.Chats.Mode != "auto" || !c.Chats.StartInDryRun {
		t.Fatalf("unexpected config: %+v", c)
	}
}

func TestValidateRejectsBadAction(t *testing.T) {
	if _, err := Load("testdata/bad_action.yaml"); err == nil {
		t.Fatal("expected validation error for action: nuke")
	}
}

func TestValidateRejectsMissingToken(t *testing.T) {
	_, err := Parse([]byte("admin_chat_id: -1\naction: mute\nchats:\n  mode: auto\n"))
	if err == nil {
		t.Fatal("expected error for empty bot_token")
	}
}

func TestDetectionDefaultsAppliedWhenUnset(t *testing.T) {
	c, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detection
	if d.TrustThreshold == nil || *d.TrustThreshold != 5 {
		t.Errorf("TrustThreshold: want 5, got %v", d.TrustThreshold)
	}
	if d.Behavior.DupThreshold == nil || *d.Behavior.DupThreshold != 3 {
		t.Errorf("DupThreshold: want 3, got %v", d.Behavior.DupThreshold)
	}
	if d.Behavior.DupWindow.Duration() != 60*time.Second {
		t.Errorf("DupWindow: want 60s, got %v", d.Behavior.DupWindow.Duration())
	}
	if d.Behavior.ShortLen == nil || *d.Behavior.ShortLen != 10 {
		t.Errorf("ShortLen: want 10, got %v", d.Behavior.ShortLen)
	}
	if d.Behavior.ShortFloodThreshold == nil || *d.Behavior.ShortFloodThreshold != 5 {
		t.Errorf("ShortFloodThreshold: want 5, got %v", d.Behavior.ShortFloodThreshold)
	}
	if d.Behavior.ShortWindow.Duration() != 30*time.Second {
		t.Errorf("ShortWindow: want 30s, got %v", d.Behavior.ShortWindow.Duration())
	}
	if d.Behavior.FlagEdits != false {
		t.Errorf("FlagEdits: want false default, got %v", d.Behavior.FlagEdits)
	}
	if d.Rules.BlockLinksForUntrusted == nil || !*d.Rules.BlockLinksForUntrusted {
		t.Errorf("BlockLinksForUntrusted: want default true, got %v", d.Rules.BlockLinksForUntrusted)
	}
	if d.BayesEnabled == nil || !*d.BayesEnabled {
		t.Errorf("BayesEnabled: want default true, got %v", d.BayesEnabled)
	}
	if d.BayesThreshold == nil || *d.BayesThreshold != 0.0 {
		t.Errorf("BayesThreshold: want default 0.0, got %v", d.BayesThreshold)
	}
	if d.BayesVocabGuess != 5000 {
		t.Errorf("BayesVocabGuess: want default 5000, got %v", d.BayesVocabGuess)
	}
	if d.FakeAdminEnabled == nil || !*d.FakeAdminEnabled {
		t.Errorf("FakeAdminEnabled: want default true, got %v", d.FakeAdminEnabled)
	}
	if d.FakeAdminMaxDistance != 1 {
		t.Errorf("FakeAdminMaxDistance: want default 1, got %v", d.FakeAdminMaxDistance)
	}
	wantTags := []string{"admin", "support", "verified", "moderator"}
	if len(d.FakeAdminSuspiciousTags) != len(wantTags) {
		t.Errorf("FakeAdminSuspiciousTags: want %v, got %v", wantTags, d.FakeAdminSuspiciousTags)
	} else {
		for i, tag := range wantTags {
			if d.FakeAdminSuspiciousTags[i] != tag {
				t.Errorf("FakeAdminSuspiciousTags: want %v, got %v", wantTags, d.FakeAdminSuspiciousTags)
				break
			}
		}
	}
	if d.AdminCacheTTLSeconds != 300 {
		t.Errorf("AdminCacheTTLSeconds: want default 300, got %v", d.AdminCacheTTLSeconds)
	}
	if d.ReactionCleanupEnabled == nil || !*d.ReactionCleanupEnabled {
		t.Errorf("ReactionCleanupEnabled: want default true, got %v", d.ReactionCleanupEnabled)
	}
	if d.EphemeralNoticeEnabled == nil || *d.EphemeralNoticeEnabled {
		t.Errorf("EphemeralNoticeEnabled: want default false, got %v", d.EphemeralNoticeEnabled)
	}
	if d.EphemeralNoticeText != "" {
		t.Errorf("EphemeralNoticeText: want default \"\", got %q", d.EphemeralNoticeText)
	}
}

func TestDetectionExplicitValuesNotOverridden(t *testing.T) {
	c, err := Load("testdata/detection_custom.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detection
	if d.TrustThreshold == nil || *d.TrustThreshold != 8 {
		t.Errorf("TrustThreshold: want 8, got %v", d.TrustThreshold)
	}
	if d.Behavior.DupThreshold == nil || *d.Behavior.DupThreshold != 2 {
		t.Errorf("DupThreshold: want 2, got %v", d.Behavior.DupThreshold)
	}
	if d.Behavior.DupWindow.Duration() != 90*time.Second {
		t.Errorf("DupWindow: want 90s, got %v", d.Behavior.DupWindow.Duration())
	}
	if d.Behavior.FlagEdits != true {
		t.Errorf("FlagEdits: want true (explicit), got %v", d.Behavior.FlagEdits)
	}
	if d.Rules.BlockLinksForUntrusted == nil || *d.Rules.BlockLinksForUntrusted {
		t.Errorf("BlockLinksForUntrusted: want explicit false, got %v", d.Rules.BlockLinksForUntrusted)
	}
	if len(d.Rules.DenyStopwords) != 1 || d.Rules.DenyStopwords[0] != "casino" {
		t.Errorf("DenyStopwords: want [casino], got %v", d.Rules.DenyStopwords)
	}
	if d.BayesEnabled == nil || *d.BayesEnabled {
		t.Errorf("BayesEnabled: want explicit false, got %v", d.BayesEnabled)
	}
	if d.BayesThreshold == nil || *d.BayesThreshold != 2.5 {
		t.Errorf("BayesThreshold: want explicit 2.5, got %v", d.BayesThreshold)
	}
	if d.BayesVocabGuess != 8000 {
		t.Errorf("BayesVocabGuess: want explicit 8000, got %v", d.BayesVocabGuess)
	}
}

// TestDetectionExplicitZeroThresholdsHonored guards against the classic
// zero-vs-unset bug: an explicit 0 for dup_threshold/short_flood_threshold
// is a documented way to disable that check (see config.example.yaml and
// detect.CheckBehavior, which treats <= 0 as disabled). Defaults must not
// clobber that explicit 0 back to the non-zero default.
func TestDetectionExplicitZeroThresholdsHonored(t *testing.T) {
	c, err := Load("testdata/detection_zero_thresholds.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detection
	if d.Behavior.DupThreshold == nil || *d.Behavior.DupThreshold != 0 {
		t.Errorf("DupThreshold: want explicit 0 honored, got %v", d.Behavior.DupThreshold)
	}
	if d.Behavior.ShortFloodThreshold == nil || *d.Behavior.ShortFloodThreshold != 0 {
		t.Errorf("ShortFloodThreshold: want explicit 0 honored, got %v", d.Behavior.ShortFloodThreshold)
	}
	// Fields left unset in this same file must still get their defaults,
	// proving the fix doesn't disable defaulting altogether.
	if d.TrustThreshold == nil || *d.TrustThreshold != 5 {
		t.Errorf("TrustThreshold: want default 5 (unset in this file), got %v", d.TrustThreshold)
	}
	if d.Behavior.ShortLen == nil || *d.Behavior.ShortLen != 10 {
		t.Errorf("ShortLen: want default 10 (unset in this file), got %v", d.Behavior.ShortLen)
	}
	// bayes_threshold: 0.0 is explicit in this file, same value as the
	// default, but must still come out non-nil via the pointer (proving
	// applyDetectionDefaults' nil-check, not a `== 0` check, gates it).
	if d.BayesThreshold == nil || *d.BayesThreshold != 0.0 {
		t.Errorf("BayesThreshold: want explicit 0.0 honored, got %v", d.BayesThreshold)
	}
	// BayesEnabled/BayesVocabGuess are left unset in this file and must
	// still get their defaults.
	if d.BayesEnabled == nil || !*d.BayesEnabled {
		t.Errorf("BayesEnabled: want default true (unset in this file), got %v", d.BayesEnabled)
	}
	if d.BayesVocabGuess != 5000 {
		t.Errorf("BayesVocabGuess: want default 5000 (unset in this file), got %v", d.BayesVocabGuess)
	}
}

// TestM5FakeAdminExplicitEmptyTagsHonored guards against the classic
// nil-vs-empty bug: an explicit empty list for fake_admin_suspicious_tags
// disables the suspicious-tag check and must not be re-defaulted back to
// the non-empty default list. fake_admin_enabled: false must also be
// honored rather than re-promoted to the default true.
func TestM5FakeAdminExplicitEmptyTagsHonored(t *testing.T) {
	c, err := Load("testdata/m5_custom.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detection
	if d.FakeAdminEnabled == nil || *d.FakeAdminEnabled {
		t.Errorf("FakeAdminEnabled: want explicit false, got %v", d.FakeAdminEnabled)
	}
	if d.FakeAdminSuspiciousTags == nil {
		t.Error("FakeAdminSuspiciousTags: want explicit empty non-nil slice, got nil")
	}
	if len(d.FakeAdminSuspiciousTags) != 0 {
		t.Errorf("FakeAdminSuspiciousTags: want explicit empty (len 0), got %v", d.FakeAdminSuspiciousTags)
	}
}

// TestM5EphemeralNoticeExplicitValuesHonored checks that an explicit
// ephemeral_notice_enabled: true plus ephemeral_notice_text are both
// honored rather than clobbered by defaults.
func TestM5EphemeralNoticeExplicitValuesHonored(t *testing.T) {
	c, err := Load("testdata/m5_ephemeral.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detection
	if d.EphemeralNoticeEnabled == nil || !*d.EphemeralNoticeEnabled {
		t.Errorf("EphemeralNoticeEnabled: want explicit true, got %v", d.EphemeralNoticeEnabled)
	}
	if d.EphemeralNoticeText != "removed" {
		t.Errorf("EphemeralNoticeText: want explicit \"removed\", got %q", d.EphemeralNoticeText)
	}
}

const baseValidYAML = "bot_token: \"12345:AA\"\n" +
	"admin_chat_id: -1009999\n" +
	"action: delete_mute\n" +
	"chats:\n" +
	"  mode: auto\n"

// TestBlocklistDefaultsAppliedWhenUnset checks that omitting the blocklist:
// block entirely still yields a fully-populated Blocklist config, with the
// documented defaults (see config.example.yaml).
func TestBlocklistDefaultsAppliedWhenUnset(t *testing.T) {
	c, err := Parse([]byte(baseValidYAML))
	if err != nil {
		t.Fatal(err)
	}
	bl := c.Blocklist
	if bl.Enabled == nil || !*bl.Enabled {
		t.Errorf("Enabled: want default true, got %v", bl.Enabled)
	}
	if bl.LolsFullURL != "https://lols.bot/spam/banlist.txt" {
		t.Errorf("LolsFullURL: want default, got %q", bl.LolsFullURL)
	}
	if bl.LolsDeltaURL != "https://lols.bot/spam/banlist-1h.txt" {
		t.Errorf("LolsDeltaURL: want default, got %q", bl.LolsDeltaURL)
	}
	if bl.CasFullURL != "https://api.cas.chat/export.csv" {
		t.Errorf("CasFullURL: want default, got %q", bl.CasFullURL)
	}
	if bl.FullRefresh.Duration() != 6*time.Hour {
		t.Errorf("FullRefresh: want 6h default, got %v", bl.FullRefresh.Duration())
	}
	if bl.DeltaRefresh.Duration() != 1*time.Hour {
		t.Errorf("DeltaRefresh: want 1h default, got %v", bl.DeltaRefresh.Duration())
	}
	if bl.HTTPTimeout.Duration() != 30*time.Second {
		t.Errorf("HTTPTimeout: want 30s default, got %v", bl.HTTPTimeout.Duration())
	}
}

// TestBlocklistExplicitValuesNotOverridden checks that explicit
// blocklist.enabled: false and blocklist.full_refresh: 12h are honored
// rather than clobbered by defaults, while the fields left unset in this
// YAML still get their documented defaults.
func TestBlocklistExplicitValuesNotOverridden(t *testing.T) {
	yaml := baseValidYAML +
		"blocklist:\n" +
		"  enabled: false\n" +
		"  full_refresh: 12h\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	bl := c.Blocklist
	if bl.Enabled == nil || *bl.Enabled {
		t.Errorf("Enabled: want explicit false, got %v", bl.Enabled)
	}
	if bl.FullRefresh.Duration() != 12*time.Hour {
		t.Errorf("FullRefresh: want explicit 12h, got %v", bl.FullRefresh.Duration())
	}
	if bl.LolsFullURL != "https://lols.bot/spam/banlist.txt" {
		t.Errorf("LolsFullURL: want default, got %q", bl.LolsFullURL)
	}
	if bl.LolsDeltaURL != "https://lols.bot/spam/banlist-1h.txt" {
		t.Errorf("LolsDeltaURL: want default, got %q", bl.LolsDeltaURL)
	}
	if bl.CasFullURL != "https://api.cas.chat/export.csv" {
		t.Errorf("CasFullURL: want default, got %q", bl.CasFullURL)
	}
	if bl.DeltaRefresh.Duration() != 1*time.Hour {
		t.Errorf("DeltaRefresh: want 1h default, got %v", bl.DeltaRefresh.Duration())
	}
	if bl.HTTPTimeout.Duration() != 30*time.Second {
		t.Errorf("HTTPTimeout: want 30s default, got %v", bl.HTTPTimeout.Duration())
	}
}

// TestOpsDefaultsAppliedWhenUnset checks that omitting the ops: block
// entirely still yields a fully-populated Ops config, with the documented
// defaults (see config.example.yaml).
func TestOpsDefaultsAppliedWhenUnset(t *testing.T) {
	c, err := Parse([]byte(baseValidYAML))
	if err != nil {
		t.Fatal(err)
	}
	o := c.Ops
	if o.MetricsEnabled == nil || !*o.MetricsEnabled {
		t.Errorf("MetricsEnabled: want default true, got %v", o.MetricsEnabled)
	}
	if o.MetricsAddr != ":9090" {
		t.Errorf("MetricsAddr: want default \":9090\", got %q", o.MetricsAddr)
	}
	if o.DigestEnabled == nil || !*o.DigestEnabled {
		t.Errorf("DigestEnabled: want default true, got %v", o.DigestEnabled)
	}
	if o.DigestInterval.Duration() != 24*time.Hour {
		t.Errorf("DigestInterval: want 24h default, got %v", o.DigestInterval.Duration())
	}
}

// TestOpsExplicitValuesNotOverridden checks that explicit ops.metrics_enabled:
// false and ops.metrics_addr are honored rather than clobbered by defaults,
// while the fields left unset in this YAML still get their documented
// defaults.
func TestOpsExplicitValuesNotOverridden(t *testing.T) {
	yaml := baseValidYAML +
		"ops:\n" +
		"  metrics_enabled: false\n" +
		"  metrics_addr: \":1234\"\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	o := c.Ops
	if o.MetricsEnabled == nil || *o.MetricsEnabled {
		t.Errorf("MetricsEnabled: want explicit false, got %v", o.MetricsEnabled)
	}
	if o.MetricsAddr != ":1234" {
		t.Errorf("MetricsAddr: want explicit \":1234\", got %q", o.MetricsAddr)
	}
	if o.DigestEnabled == nil || !*o.DigestEnabled {
		t.Errorf("DigestEnabled: want default true, got %v", o.DigestEnabled)
	}
	if o.DigestInterval.Duration() != 24*time.Hour {
		t.Errorf("DigestInterval: want 24h default, got %v", o.DigestInterval.Duration())
	}
}

// TestOpsDigestIntervalClampedWhenNonPositive checks that a non-positive
// digest_interval is clamped to the 24h default rather than left at a value
// that would panic time.NewTicker (see Blocklist's FullRefresh/DeltaRefresh
// for the same M6 lesson).
func TestOpsDigestIntervalClampedWhenNonPositive(t *testing.T) {
	yaml := baseValidYAML +
		"ops:\n" +
		"  digest_interval: -5m\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if c.Ops.DigestInterval.Duration() != 24*time.Hour {
		t.Errorf("DigestInterval: want clamped to 24h default, got %v", c.Ops.DigestInterval.Duration())
	}
}

func TestLLMDefaultsDisabledByDefault(t *testing.T) {
	c, err := Parse([]byte(baseValidYAML))
	if err != nil {
		t.Fatal(err)
	}
	l := c.LLM
	if l.Enabled == nil || *l.Enabled {
		t.Errorf("LLM.Enabled: want default false, got %v", l.Enabled)
	}
	if l.Policy != "any" {
		t.Errorf("LLM.Policy: want default any, got %q", l.Policy)
	}
	if l.BorderlineBand != 0.5 {
		t.Errorf("LLM.BorderlineBand: want default 0.5, got %v", l.BorderlineBand)
	}
	if l.HTTPTimeout.Duration() != 10*time.Second {
		t.Errorf("LLM.HTTPTimeout: want default 10s, got %v", l.HTTPTimeout.Duration())
	}
}

func TestLLMEnabledValid(t *testing.T) {
	yaml := baseValidYAML +
		"llm:\n" +
		"  enabled: true\n" +
		"  policy: all\n" +
		"  borderline_band: 1.5\n" +
		"  providers:\n" +
		"    - kind: openai\n" +
		"      api_key: sk-x\n" +
		"      model: gpt-4o-mini\n" +
		"    - kind: anthropic\n" +
		"      api_key: ak-x\n" +
		"      model: claude-3-5-haiku-latest\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.Enabled == nil || !*c.LLM.Enabled || c.LLM.Policy != "all" || len(c.LLM.Providers) != 2 {
		t.Fatalf("unexpected LLM config: %+v", c.LLM)
	}
	if c.LLM.BorderlineBand != 1.5 {
		t.Errorf("BorderlineBand: want explicit 1.5, got %v", c.LLM.BorderlineBand)
	}
}

func TestLLMEnabledValidationErrors(t *testing.T) {
	cases := map[string]string{
		"no providers":  "llm:\n  enabled: true\n",
		"bad policy":    "llm:\n  enabled: true\n  policy: majority\n  providers:\n    - kind: openai\n      api_key: k\n      model: m\n",
		"bad kind":      "llm:\n  enabled: true\n  providers:\n    - kind: cohere\n      api_key: k\n      model: m\n",
		"missing key":   "llm:\n  enabled: true\n  providers:\n    - kind: openai\n      model: m\n",
		"missing model": "llm:\n  enabled: true\n  providers:\n    - kind: openai\n      api_key: k\n",
	}
	for name, frag := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(baseValidYAML + frag)); err == nil {
				t.Fatalf("%s: expected validation error", name)
			}
		})
	}
}

func TestLLMDisabledSkipsValidation(t *testing.T) {
	// enabled:false with a bogus policy must still parse — validation only
	// applies when the stage is on.
	yaml := baseValidYAML + "llm:\n  enabled: false\n  policy: nonsense\n"
	if _, err := Parse([]byte(yaml)); err != nil {
		t.Fatalf("disabled LLM should skip validation, got %v", err)
	}
}

func TestBotTokenEnvOverride(t *testing.T) {
	t.Setenv("BOT_TOKEN", "env-token-123")
	// baseValidYAML sets a bot_token; env must win over it.
	c, err := Parse([]byte(baseValidYAML))
	if err != nil {
		t.Fatal(err)
	}
	if c.BotToken != "env-token-123" {
		t.Fatalf("BOT_TOKEN env should win, got %q", c.BotToken)
	}
}

func TestBotTokenFromEnvWhenFileEmpty(t *testing.T) {
	t.Setenv("BOT_TOKEN", "env-only-token")
	// A config with no bot_token must still validate when BOT_TOKEN is set.
	yaml := "admin_chat_id: -100123\naction: delete_mute\nchats:\n  mode: auto\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("expected valid config from env token, got %v", err)
	}
	if c.BotToken != "env-only-token" {
		t.Fatalf("got %q", c.BotToken)
	}
}
