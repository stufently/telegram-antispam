package config

import "testing"

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
