package config

import (
	"strings"
	"testing"
	"time"
)

const mediaShadowConfig = `bot_token: test
admin_chat_id: -1
action: mute
chats:
  mode: auto
detection:
  media_caption_min_len: 20
  media_shadow:
    enabled: true
`

func TestMediaShadowDefaultsAndEnvironmentKey(t *testing.T) {
	t.Setenv("JEV_TOKEN", "env-token")
	c, err := Parse([]byte(mediaShadowConfig))
	if err != nil {
		t.Fatal(err)
	}
	ms := c.Detection.MediaShadow
	if !ms.Enabled || ms.APIKey != "env-token" || ms.Model != "jev-latest" || ms.Timeout.Duration() != 8*time.Second {
		t.Fatalf("defaults: %+v", ms)
	}
	t.Setenv("JEV_TOKEN", "")
	c, err = Parse([]byte(mediaShadowConfig + "    api_key: yaml\n    model: jev-1.14.0\n    timeout: 3s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ms := c.Detection.MediaShadow; ms.APIKey != "yaml" || ms.Model != "jev-1.14.0" || ms.Timeout.Duration() != 3*time.Second {
		t.Fatalf("explicit: %+v", ms)
	}
	off, err := Parse([]byte("bot_token: test\nadmin_chat_id: -1\naction: mute\nchats:\n  mode: auto\n"))
	if err != nil || off.Detection.MediaShadow.Enabled {
		t.Fatalf("enabled by default: %v", err)
	}
}

func TestMediaShadowValidation(t *testing.T) {
	t.Setenv("JEV_TOKEN", "")
	for name, cfg := range map[string]string{
		"timeout too long": mediaShadowConfig + "    timeout: 31s\n",
		"negative timeout": mediaShadowConfig + "    timeout: -1s\n",
		"no caption stage": strings.Replace(mediaShadowConfig, "media_caption_min_len: 20", "media_caption_min_len: 0", 1),
	} {
		if _, err := Parse([]byte(cfg)); err == nil || !strings.Contains(err.Error(), "media_shadow") {
			t.Fatalf("%s: want media_shadow error, got %v", name, err)
		}
	}
	// 30s is the inclusive ceiling.
	if _, err := Parse([]byte(mediaShadowConfig + "    timeout: 30s\n")); err != nil {
		t.Fatal(err)
	}
	if err := UnknownKeys([]byte(mediaShadowConfig + "    api_base: https://x\n")); err != nil {
		t.Fatalf("keys not known: %v", err)
	}
}
