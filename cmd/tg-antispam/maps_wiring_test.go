package main

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestRulesFromConfigWiresAllowGoogleMapsLinks(t *testing.T) {
	enabled, err := config.Parse([]byte(`bot_token: "t"
admin_chat_id: -1
action: mute
chats:
  mode: auto
detection:
  rules:
    allow_google_maps_links: true
`))
	if err != nil {
		t.Fatal(err)
	}
	rules := rulesFromConfig(enabled)
	if !rules.AllowGoogleMapsLinks {
		t.Fatal("explicit true must be copied onto detect.Rules")
	}
	msg := domain.Message{Text: "https://maps.google.com/maps"}
	n := detect.Normalize(msg)
	if sig, hit := rules.Check(n, false); hit {
		t.Fatalf("wired true must not emit link_from_untrusted for Maps, got %+v", sig)
	}

	unset, err := config.Parse([]byte(`bot_token: "t"
admin_chat_id: -1
action: mute
chats:
  mode: auto
`))
	if err != nil {
		t.Fatal(err)
	}
	defaultRules := rulesFromConfig(unset)
	if defaultRules.AllowGoogleMapsLinks {
		t.Fatal("unset key must wire as false")
	}
	if sig, hit := defaultRules.Check(n, false); !hit || sig.Name != "link_from_untrusted" {
		t.Fatalf("default wiring must still block Maps, got hit=%v sig=%+v", hit, sig)
	}
}
