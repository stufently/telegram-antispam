package config

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func bp(v bool) *bool { return &v }

func TestWelcomeDefaultsOff(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	c, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if c.Welcome.Enabled == nil || *c.Welcome.Enabled || c.Welcome.MaxPerMinute == nil || *c.Welcome.MaxPerMinute != 20 {
		t.Fatalf("defaults enabled=%v max=%v", c.Welcome.Enabled, c.Welcome.MaxPerMinute)
	}
	if _, ok := c.Welcome.For(-100); ok {
		t.Fatal("empty welcome must stay off")
	}
	c, err = Parse([]byte(base + "welcome:\n  enabled: false\n  max_per_minute: 7\n"))
	if err != nil || c.Welcome.Enabled == nil || *c.Welcome.Enabled || *c.Welcome.MaxPerMinute != 7 {
		t.Fatalf("explicit false/7 overwritten: %+v err=%v", c.Welcome, err)
	}
	c, err = Parse([]byte(base + "welcome:\n  enabled: true\n  text: hi\n  max_per_minute: 1\n"))
	if err != nil || c.Welcome.Enabled == nil || !*c.Welcome.Enabled || *c.Welcome.MaxPerMinute != 1 {
		t.Fatalf("explicit true/1 overwritten: %+v err=%v", c.Welcome, err)
	}
}

func TestWelcomePerChatResolution(t *testing.T) {
	yaml := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n" +
		"welcome:\n  enabled: true\n  text: \"общий\"\n  chats:\n" +
		"    -1001:\n      enabled: false\n      text: \"скрытый\"\n" +
		"    -1002:\n      enabled: true\n      text: \"  свой  \"\n" +
		"    -1003:\n      text: \"только текст\"\n" +
		"    -1005:\n      enabled: true\n      text: \"   \"\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		id   int64
		text string
		ok   bool
	}{
		{-1001, "", false},
		{-1002, "свой", true},
		{-1003, "только текст", true},
		{-1004, "общий", true},
		{-1005, "общий", true},
	}
	for _, tt := range checks {
		text, ok := c.Welcome.For(tt.id)
		if ok != tt.ok || (tt.ok && text != tt.text) {
			t.Fatalf("chat %d = %q ok=%v, want %q ok=%v", tt.id, text, ok, tt.text, tt.ok)
		}
	}
	off := *c
	off.Welcome.Enabled = bp(false)
	if _, ok := off.Welcome.For(-1002); !ok {
		t.Fatal("per-chat enabled must beat a global off switch")
	}
	if text, ok := off.Welcome.For(-1002); !ok || text != "свой" {
		t.Fatalf("per-chat text = %q ok=%v", text, ok)
	}
	if _, ok := off.Welcome.For(-1003); ok {
		t.Fatal("text alone must not turn a chat on when the global switch is off")
	}
	if _, ok := off.Welcome.For(-1004); ok {
		t.Fatal("unlisted chat must follow the global switch")
	}
}

func TestWelcomeValidate(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	long := strings.Repeat("я", 4097)
	if utf8.RuneCountInString(long) != 4097 {
		t.Fatal(utf8.RuneCountInString(long))
	}
	cases := []struct{ name, yaml, key string }{
		{"max zero", base + "welcome:\n  max_per_minute: 0\n", "welcome.max_per_minute"},
		{"max negative", base + "welcome:\n  max_per_minute: -3\n", "welcome.max_per_minute"},
		{"empty global text", base + "welcome:\n  enabled: true\n  text: \"  \"\n", "welcome.text"},
		{"empty chat text", base + "welcome:\n  enabled: false\n  chats:\n    -100:\n      enabled: true\n", "welcome.chats"},
		{"global text too long", base + "welcome:\n  text: \"" + long + "\"\n", "welcome.text"},
		{"chat text too long", base + "welcome:\n  chats:\n    -100:\n      text: \"" + long + "\"\n", "welcome.chats"},
		{"outside allowlist", "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: allowlist\n  allowlist: [-100]\nwelcome:\n  chats:\n    -200:\n      enabled: false\n", "welcome.chats"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("err=%v, want key %s", err, tt.key)
			}
		})
	}
	ok := []string{
		base + "welcome:\n  enabled: true\n  text: \"" + strings.Repeat("я", 4096) + "\"\n  max_per_minute: 1\n",
		"bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: allowlist\n  allowlist: [-100]\nwelcome:\n  chats:\n    -100:\n      enabled: true\n      text: hi\n",
		base + "welcome:\n  chats:\n    -200:\n      enabled: true\n      text: hi\n",
	}
	for _, y := range ok {
		if _, err := Parse([]byte(y)); err != nil {
			t.Fatalf("valid config rejected: %v", err)
		}
	}
}

func TestConfigExampleParsesWithWelcomeOff(t *testing.T) {
	b, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := UnknownKeys(b); err != nil {
		t.Fatal(err)
	}
	c, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Welcome.Enabled == nil || *c.Welcome.Enabled {
		t.Fatalf("enabled=%v", c.Welcome.Enabled)
	}
	if _, ok := c.Welcome.For(-1001234567890); ok {
		t.Fatal("example must not greet any chat")
	}
}

func TestWelcomeForTrimsGlobalText(t *testing.T) {
	w := Welcome{Enabled: bp(true), Text: " \tобщий\n "}
	text, ok := w.For(-100)
	if !ok || text != "общий" {
		t.Fatalf("For(-100)=(%q, %v), want (%q, true)", text, ok, "общий")
	}
}

func TestWelcomeChatTextAtLimit(t *testing.T) {
	text := strings.Repeat("я", 4096)
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	yaml := base + "welcome:\n  enabled: false\n  chats:\n    -100:\n      enabled: true\n      text: \"" + text + "\"\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("4096-rune per-chat welcome rejected: %v", err)
	}
	got, ok := c.Welcome.For(-100)
	if !ok || got != text {
		t.Fatalf("For(-100)=(%q, %v), want full 4096-rune text and true", got, ok)
	}
}
