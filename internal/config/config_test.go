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
