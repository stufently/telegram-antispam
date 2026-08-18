package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchReloadsOnValidChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(p, []byte("bot_token: t\nadmin_chat_id: -1\naction: mute\nchats:\n  mode: auto\n"), 0o600))

	c, err := Load(p)
	must(err)
	s := NewStore(c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Watch(ctx, p)

	// give the watcher a moment to register before writing
	time.Sleep(100 * time.Millisecond)
	must(os.WriteFile(p, []byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"), 0o600))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.Current().Action == ActionBan {
			return // reloaded successfully
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("config was not hot-reloaded within 5s, still %v", s.Current().Action)
}

func TestWatchKeepsConfigOnInvalidChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte("bot_token: t\nadmin_chat_id: -1\naction: mute\nchats:\n  mode: auto\n"), 0o600)

	c, _ := Load(p)
	s := NewStore(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Watch(ctx, p)
	time.Sleep(100 * time.Millisecond)

	// write an invalid config; the running config must be kept
	os.WriteFile(p, []byte("action: nuke\nchats:\n  mode: auto\n"), 0o600)
	time.Sleep(1 * time.Second)
	if s.Current().Action != ActionMute {
		t.Fatalf("running config changed on invalid reload: %v", s.Current().Action)
	}
}
