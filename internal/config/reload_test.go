package config

import (
	"os"
	"path/filepath"
	"testing"
)

func base(action string) *Config {
	return &Config{BotToken: "t", AdminChatID: -1, Action: Action(action), Chats: ChatsPolicy{Mode: "auto"}}
}

func TestReloadKeepsOldConfigOnInvalidFile(t *testing.T) {
	s := NewStore(base("mute"))
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("action: nuke\nchats:\n  mode: auto\n"), 0o600)

	if err := s.tryReload(p); err == nil {
		t.Fatal("expected reload to reject invalid file")
	}
	if s.Current().Action != ActionMute {
		t.Fatalf("running config changed on bad reload: %v", s.Current().Action)
	}
}

func TestReloadSwapsOnValidFile(t *testing.T) {
	s := NewStore(base("mute"))
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"), 0o600)

	if err := s.tryReload(p); err != nil {
		t.Fatal(err)
	}
	if s.Current().Action != ActionBan {
		t.Fatalf("config not swapped, got %v", s.Current().Action)
	}
}
