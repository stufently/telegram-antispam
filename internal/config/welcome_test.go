package config

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWelcomeDefaultsOff(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	c, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if c.Welcome.Enabled == nil || *c.Welcome.Enabled {
		t.Fatalf("omitted welcome.enabled = %v, want false", c.Welcome.Enabled)
	}
	if c.Welcome.MaxPerMinute == nil || *c.Welcome.MaxPerMinute != 20 {
		t.Fatalf("omitted welcome.max_per_minute = %v, want 20", c.Welcome.MaxPerMinute)
	}
	if _, ok := c.Welcome.For(-100); ok {
		t.Fatal("welcome with no text must stay off")
	}

	c, err = Parse([]byte(base + "welcome:\n  enabled: false\n  max_per_minute: 7\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Welcome.Enabled == nil || *c.Welcome.Enabled {
		t.Fatal("explicit welcome.enabled false was overwritten")
	}
	if c.Welcome.MaxPerMinute == nil || *c.Welcome.MaxPerMinute != 7 {
		t.Fatalf("explicit max_per_minute = %v, want 7", c.Welcome.MaxPerMinute)
	}

	c, err = Parse([]byte(base + "welcome:\n  enabled: true\n  text: hi\n  max_per_minute: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Welcome.Enabled == nil || !*c.Welcome.Enabled {
		t.Fatal("explicit welcome.enabled true was overwritten with the default")
	}
	if c.Welcome.MaxPerMinute == nil || *c.Welcome.MaxPerMinute != 1 {
		t.Fatalf("explicit max_per_minute = %v, want 1", c.Welcome.MaxPerMinute)
	}
}

func TestWelcomePerChatResolution(t *testing.T) {
	on := `
bot_token: t
admin_chat_id: -1
action: ban
chats:
  mode: auto
welcome:
  enabled: true
  text: "общий"
  chats:
    -1001:
      enabled: false
      text: "скрытый"
    -1002:
      enabled: true
      text: "  свой  "
    -1003:
      text: "только текст"
    -1005:
      enabled: true
      text: "   "
`
	c, err := Parse([]byte(on))
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := c.Welcome.For(-1001); ok {
		t.Fatalf("chat disabled in the map is on, text %q", text)
	}
	if text, ok := c.Welcome.For(-1002); !ok || text != "свой" {
		t.Fatalf("per-chat text = %q ok=%v, want %q", text, ok, "свой")
	}
	if text, ok := c.Welcome.For(-1003); !ok || text != "только текст" {
		t.Fatalf("text-only override = %q ok=%v, want the override while the global switch is on", text, ok)
	}
	if text, ok := c.Welcome.For(-1004); !ok || text != "общий" {
		t.Fatalf("unlisted chat = %q ok=%v, want the global text", text, ok)
	}
	if text, ok := c.Welcome.For(-1005); !ok || text != "общий" {
		t.Fatalf("blank per-chat text = %q ok=%v, want the global text", text, ok)
	}

	off := `
bot_token: t
admin_chat_id: -1
action: ban
chats:
  mode: auto
welcome:
  enabled: false
  text: "общий"
  chats:
    -1002:
      enabled: true
      text: "свой"
    -1003:
      text: "только текст"
`
	c, err = Parse([]byte(off))
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := c.Welcome.For(-1002); !ok || text != "свой" {
		t.Fatalf("per-chat enabled = %q ok=%v, want it on despite the global switch", text, ok)
	}
	if _, ok := c.Welcome.For(-1003); ok {
		t.Fatal("a text override must not turn a chat on when its Enabled is unset and the global switch is off")
	}
	if _, ok := c.Welcome.For(-1004); ok {
		t.Fatal("unlisted chat must follow the global switch")
	}
}

func TestWelcomeValidate(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	long := strings.Repeat("я", 4097)
	if utf8.RuneCountInString(long) != 4097 {
		t.Fatalf("fixture is %d runes", utf8.RuneCountInString(long))
	}

	cases := []struct {
		name string
		yaml string
		key  string
	}{
		{
			name: "max per minute zero",
			yaml: base + "welcome:\n  max_per_minute: 0\n",
			key:  "welcome.max_per_minute",
		},
		{
			name: "max per minute negative",
			yaml: base + "welcome:\n  max_per_minute: -3\n",
			key:  "welcome.max_per_minute",
		},
		{
			name: "global switch with empty text",
			yaml: base + "welcome:\n  enabled: true\n  text: \"  \"\n",
			key:  "welcome.text",
		},
		{
			name: "per-chat switch with empty text",
			yaml: base + "welcome:\n  enabled: false\n  chats:\n    -100:\n      enabled: true\n      text: \"\"\n",
			key:  "welcome.chats",
		},
		{
			name: "global text over the rune limit",
			yaml: base + "welcome:\n  enabled: false\n  text: \"" + long + "\"\n",
			key:  "welcome.text",
		},
		{
			name: "per-chat text over the rune limit",
			yaml: base + "welcome:\n  enabled: false\n  chats:\n    -100:\n      enabled: false\n      text: \"" + long + "\"\n",
			key:  "welcome.chats",
		},
		{
			name: "welcome chat outside the allowlist",
			yaml: "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: allowlist\n  allowlist: [-100]\nwelcome:\n  enabled: false\n  chats:\n    -200:\n      enabled: false\n",
			key:  "welcome.chats",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("err = %v, want it to name %s", err, tt.key)
			}
		})
	}

	// The same keys are accepted when they satisfy the rule.
	if _, err := Parse([]byte(base + "welcome:\n  enabled: true\n  text: \"" + strings.Repeat("я", 4096) + "\"\n  max_per_minute: 1\n")); err != nil {
		t.Fatalf("text of exactly 4096 runes rejected: %v", err)
	}
	if _, err := Parse([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: allowlist\n  allowlist: [-100]\nwelcome:\n  chats:\n    -100:\n      enabled: true\n      text: hi\n")); err != nil {
		t.Fatalf("welcome chat inside the allowlist rejected: %v", err)
	}
	if _, err := Parse([]byte(base + "welcome:\n  enabled: false\n  chats:\n    -200:\n      enabled: true\n      text: hi\n")); err != nil {
		t.Fatalf("welcome chat in auto mode rejected: %v", err)
	}
}

func TestConfigExampleParsesWithWelcomeOff(t *testing.T) {
	b, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := UnknownKeys(b); err != nil {
		t.Fatalf("UnknownKeys: %v", err)
	}
	c, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Welcome.Enabled == nil || *c.Welcome.Enabled {
		t.Fatalf("example welcome.enabled = %v, want false", c.Welcome.Enabled)
	}
	if _, ok := c.Welcome.For(-1001234567890); ok {
		t.Fatal("example config must not greet any chat")
	}
}
