package config

import (
	"strings"
	"testing"
)

func TestUnknownKeysSpotsATypo(t *testing.T) {
	// The trap: yaml.v3 accepts this happily and the setting never applies.
	err := UnknownKeys([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n  enforse: [-100]\n"))
	if err == nil {
		t.Fatal("a misspelled key must be reported, not silently dropped")
	}
	if !strings.Contains(err.Error(), "enforse") {
		t.Fatalf("error must name the offending key, got: %v", err)
	}
}

func TestUnknownKeysAcceptsAValidConfig(t *testing.T) {
	if err := UnknownKeys([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n  enforce: [-100]\n")); err != nil {
		t.Fatalf("valid config reported as unknown: %v", err)
	}
}
