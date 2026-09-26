package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCaptchaDefaultsOff(t *testing.T) {
	c, err := Parse([]byte("bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Captcha.Enabled == nil || *c.Captcha.Enabled {
		t.Fatal(c.Captcha.Enabled)
	}
	if c.Captcha.Mode != "button" || c.Captcha.OnFail != "kick" {
		t.Fatal(c.Captcha.Mode, c.Captcha.OnFail)
	}
	if c.Captcha.Timeout == nil || c.Captcha.Timeout.Duration() != 2*time.Minute {
		t.Fatal(c.Captcha.Timeout)
	}
	const text = "Press the button below to confirm you are not a bot, otherwise you will be removed from the chat."
	if c.Captcha.Text == nil || *c.Captcha.Text != text || c.Captcha.ButtonText == nil || *c.Captcha.ButtonText != "I am not a bot" {
		t.Fatal(c.Captcha.Text, c.Captcha.ButtonText)
	}
	if _, ok := c.Captcha.For(-100); ok {
		t.Fatal("default captcha is on")
	}
}

func TestCaptchaPerChatResolution(t *testing.T) {
	yaml := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\ncaptcha:\n" +
		"  enabled: true\n  timeout: 2m\n  on_fail: kick\n  text: global\n  button_text: Go\n  chats:\n" +
		"    -100:\n      timeout: 5m\n      on_fail: keep_muted\n" +
		"    -200:\n      enabled: false\n" +
		"    -300:\n      enabled: true\n      text: only\n      button_text: Tap\n"
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	pol, ok := c.Captcha.For(-100)
	if !ok || pol.Timeout != 5*time.Minute || pol.OnFail != "keep_muted" || pol.Text != "global" || pol.ButtonText != "Go" || pol.Mode != "button" {
		t.Fatal(pol, ok)
	}
	if _, ok = c.Captcha.For(-200); ok {
		t.Fatal("chat enabled false must be off")
	}
	pol, ok = c.Captcha.For(-300)
	if !ok || pol.Text != "only" || pol.ButtonText != "Tap" || pol.Timeout != 2*time.Minute || pol.OnFail != "kick" {
		t.Fatal(pol, ok)
	}
	pol, ok = c.Captcha.For(-400)
	if !ok || pol.Text != "global" || pol.Timeout != 2*time.Minute {
		t.Fatal(pol, ok)
	}
	off := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\ncaptcha:\n" +
		"  enabled: false\n  chats:\n    -100:\n      enabled: true\n      text: hi\n      button_text: btn\n"
	c, err = Parse([]byte(off))
	if err != nil {
		t.Fatal(err)
	}
	pol, ok = c.Captcha.For(-100)
	if !ok || pol.Text != "hi" || pol.ButtonText != "btn" {
		t.Fatal(pol, ok)
	}
	if _, ok = c.Captcha.For(-999); ok {
		t.Fatal("global off must stay off")
	}
}

func TestCaptchaValidate(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	longText := strings.Repeat("я", 4097)
	longButton := strings.Repeat("b", 65)
	cases := []struct{ name, yaml, key, extra string }{
		{"mode", base + "captcha:\n  mode: foo\n", "captcha.mode", "not supported"},
		{"chat mode", base + "captcha:\n  chats:\n    -100:\n      mode: foo\n", "captcha.chats", "not supported"},
		{"on_fail", base + "captcha:\n  on_fail: ban\n", "captcha.on_fail", ""},
		{"chat on_fail", base + "captcha:\n  chats:\n    -100:\n      on_fail: ban\n", "captcha.chats", ""},
		{"timeout short", base + "captcha:\n  timeout: 10s\n", "captcha.timeout", ""},
		{"timeout long", base + "captcha:\n  timeout: 2h\n", "captcha.timeout", ""},
		{"chat timeout", base + "captcha:\n  chats:\n    -100:\n      timeout: 10s\n", "captcha.chats", ""},
		{"text blank", base + "captcha:\n  enabled: true\n  text: \"  \"\n", "captcha.text", ""},
		{"text long", base + "captcha:\n  text: \"" + longText + "\"\n", "captcha.text", ""},
		{"button blank", base + "captcha:\n  enabled: true\n  button_text: \"  \"\n", "captcha.button_text", ""},
		{"button long", base + "captcha:\n  button_text: \"" + longButton + "\"\n", "captcha.button_text", ""},
		{"chat text blank", base + "captcha:\n  enabled: false\n  chats:\n    -100:\n      enabled: true\n      text: \"  \"\n", "captcha.chats", ""},
		{"allowlist", "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: allowlist\n  allowlist: [-100]\ncaptcha:\n  chats:\n    -200:\n      enabled: false\n", "captcha.chats", ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.key) || (tt.extra != "" && !strings.Contains(err.Error(), tt.extra)) {
				t.Fatal(err, tt.key, tt.extra)
			}
		})
	}
	ok := []string{
		base + "captcha:\n  timeout: 30s\n",
		base + "captcha:\n  timeout: 1h\n",
		base + "captcha:\n  enabled: true\n  text: \"" + strings.Repeat("я", 4096) + "\"\n  button_text: \"" + strings.Repeat("b", 64) + "\"\n",
		base + "captcha:\n  chats:\n    -100:\n      timeout: 30s\n",
	}
	for _, y := range ok {
		if _, err := Parse([]byte(y)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCaptchaJoinRequestModeAccepted(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	c, err := Parse([]byte(base + "captcha:\n  enabled: true\n  mode: join_request\n"))
	if err != nil || c.Captcha.Mode != "join_request" {
		t.Fatal(err, c.Captcha.Mode)
	}
	if pol, ok := c.Captcha.For(-100); !ok || pol.Mode != "join_request" {
		t.Fatal(pol, ok)
	}
	c, err = Parse([]byte(base + "captcha:\n  enabled: true\n  chats:\n    -100:\n      mode: join_request\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Captcha.Mode != "button" {
		t.Fatal(c.Captcha.Mode)
	}
	if pol, ok := c.Captcha.For(-100); !ok || pol.Mode != "join_request" {
		t.Fatal(pol, ok)
	}
	_, err = Parse([]byte(base + "captcha:\n  mode: foo\n"))
	if err == nil || !strings.Contains(err.Error(), "captcha.mode") {
		t.Fatal(err)
	}
}

func TestCaptchaValidateExplicitZeroAndEmpty(t *testing.T) {
	base := "bot_token: t\nadmin_chat_id: -1\naction: ban\nchats:\n  mode: auto\n"
	cases := []struct{ name, yaml, key string }{
		{"timeout zero", base + "captcha:\n  enabled: true\n  timeout: 0s\n  text: hi\n  button_text: Go\n", "captcha.timeout"},
		{"text empty", base + "captcha:\n  enabled: false\n  text: \"\"\n", "captcha.text"},
		{"button empty", base + "captcha:\n  button_text: \"\"\n", "captcha.button_text"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatal(err, tt.key)
			}
		})
	}
	inherit := base + "captcha:\n  enabled: true\n  timeout: 2m\n  text: hello\n  button_text: Go\n  chats:\n    -100:\n      timeout: 0s\n      text: \"\"\n      button_text: \"\"\n"
	c, err := Parse([]byte(inherit))
	if err != nil {
		t.Fatal(err)
	}
	pol, ok := c.Captcha.For(-100)
	if !ok || pol.Timeout != 2*time.Minute || pol.Text != "hello" || pol.ButtonText != "Go" {
		t.Fatal(pol, ok)
	}
}

func TestConfigExampleParsesWithCaptchaOff(t *testing.T) {
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
	if c.Captcha.Enabled == nil || *c.Captcha.Enabled {
		t.Fatal(c.Captcha.Enabled)
	}
	if _, ok := c.Captcha.For(-1001234567890); ok {
		t.Fatal("example must not challenge any chat")
	}
}
