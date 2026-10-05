package config

import (
	"testing"
	"time"
)

func TestBlocklistCASDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, yaml                  string
		enabled                     bool
		url                         string
		positive, negative, timeout time.Duration
		rate                        float64
		burst                       int
	}{
		{"empty", "blocklist: {}\n", true, "https://api.cas.chat/check", 24 * time.Hour, 6 * time.Hour, 2 * time.Second, 10, 20},
		{"disabled", "blocklist:\n  cas_check_enabled: false\n", false, "https://api.cas.chat/check", 24 * time.Hour, 6 * time.Hour, 2 * time.Second, 10, 20},
		{"explicit", "blocklist:\n  cas_check_enabled: true\n  cas_check_url: http://localhost/custom\n  cas_positive_ttl: 12h\n  cas_negative_ttl: 3h\n  cas_timeout: 500ms\n  cas_rate_per_sec: 1.5\n  cas_burst: 3\n", true, "http://localhost/custom", 12 * time.Hour, 3 * time.Hour, 500 * time.Millisecond, 1.5, 3},
		{"zero", "blocklist:\n  cas_positive_ttl: 0s\n  cas_negative_ttl: 0s\n  cas_timeout: 0s\n  cas_rate_per_sec: 0\n  cas_burst: 0\n", true, "https://api.cas.chat/check", 24 * time.Hour, 6 * time.Hour, 2 * time.Second, 10, 20},
		{"negative", "blocklist:\n  cas_positive_ttl: -1h\n  cas_negative_ttl: -1h\n  cas_timeout: -1s\n  cas_rate_per_sec: -1\n  cas_burst: -1\n", true, "https://api.cas.chat/check", 24 * time.Hour, 6 * time.Hour, 2 * time.Second, 10, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(baseValidYAML + tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			b := c.Blocklist
			if b.CasCheckEnabled == nil || *b.CasCheckEnabled != tc.enabled || b.CasCheckURL != tc.url || b.CasPositiveTTL.Duration() != tc.positive || b.CasNegativeTTL.Duration() != tc.negative || b.CasTimeout.Duration() != tc.timeout || b.CasRatePerSec != tc.rate || b.CasBurst != tc.burst {
				t.Fatalf("unexpected CAS configuration: %+v", b)
			}
		})
	}
}
