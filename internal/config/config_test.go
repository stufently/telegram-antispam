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
	if d.TrustThreshold != 5 {
		t.Errorf("TrustThreshold: want 5, got %d", d.TrustThreshold)
	}
	if d.Behavior.DupThreshold != 3 {
		t.Errorf("DupThreshold: want 3, got %d", d.Behavior.DupThreshold)
	}
	if d.Behavior.DupWindow.Duration() != 60*time.Second {
		t.Errorf("DupWindow: want 60s, got %v", d.Behavior.DupWindow.Duration())
	}
	if d.Behavior.ShortLen != 10 {
		t.Errorf("ShortLen: want 10, got %d", d.Behavior.ShortLen)
	}
	if d.Behavior.ShortFloodThreshold != 5 {
		t.Errorf("ShortFloodThreshold: want 5, got %d", d.Behavior.ShortFloodThreshold)
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
}

func TestDetectionExplicitValuesNotOverridden(t *testing.T) {
	c, err := Load("testdata/detection_custom.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := c.Detection
	if d.TrustThreshold != 8 {
		t.Errorf("TrustThreshold: want 8, got %d", d.TrustThreshold)
	}
	if d.Behavior.DupThreshold != 2 {
		t.Errorf("DupThreshold: want 2, got %d", d.Behavior.DupThreshold)
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
}
