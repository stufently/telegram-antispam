package main

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
)

func TestCASCheckActiveNeedsEnabledAndKey(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name    string
		enabled *bool
		key     string
		want    bool
	}{
		{"enabled_with_key", &on, "k", true},
		{"enabled_without_key", &on, "", false},
		{"disabled_with_key", &off, "k", false},
		{"unset", nil, "k", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := casCheckActive(config.Blocklist{CasCheckEnabled: tc.enabled, CasAPIKey: tc.key}); got != tc.want {
				t.Fatalf("casCheckActive = %v, want %v", got, tc.want)
			}
		})
	}
}
