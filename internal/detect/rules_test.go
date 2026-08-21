package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestRulesCheck(t *testing.T) {
	tests := []struct {
		name       string
		rules      Rules
		msg        NormalizedMessage
		trusted    bool
		wantSignal domain.Signal
		wantHit    bool
	}{
		// Deny stopword tests
		{
			name: "deny stopword hits",
			rules: Rules{
				DenyStopwords: []string{"crypto", "forex"},
			},
			msg: NormalizedMessage{
				Text: "buy crypto now",
			},
			trusted:    false,
			wantSignal: domain.Signal{Name: "deny_stopword", Detail: "crypto"},
			wantHit:    true,
		},
		{
			name: "deny stopword case insensitive",
			rules: Rules{
				DenyStopwords: []string{"CRYPTO"},
			},
			msg: NormalizedMessage{
				Text: "buy crypto now",
			},
			trusted:    false,
			wantSignal: domain.Signal{Name: "deny_stopword", Detail: "crypto"},
			wantHit:    true,
		},
		{
			name: "allow stopword overrides deny",
			rules: Rules{
				DenyStopwords:  []string{"crypto"},
				AllowStopwords: []string{"cryptocurrency"},
			},
			msg: NormalizedMessage{
				Text: "learn about cryptocurrency",
			},
			trusted:    false,
			wantSignal: domain.Signal{},
			wantHit:    false,
		},
		{
			name: "deny hits when allow stopword not present",
			rules: Rules{
				DenyStopwords:  []string{"crypto"},
				AllowStopwords: []string{"cryptocurrency"},
			},
			msg: NormalizedMessage{
				Text: "buy crypto now",
			},
			trusted:    false,
			wantSignal: domain.Signal{Name: "deny_stopword", Detail: "crypto"},
			wantHit:    true,
		},

		// Link policy tests
		{
			name: "link policy blocks untrusted with links",
			rules: Rules{
				BlockLinksForUntrusted: true,
			},
			msg: NormalizedMessage{
				Text:  "check this out",
				Links: []string{"https://example.com"},
			},
			trusted:    false,
			wantSignal: domain.Signal{Name: "link_from_untrusted", Detail: "example.com"},
			wantHit:    true,
		},
		{
			name: "link policy allows trusted with links",
			rules: Rules{
				BlockLinksForUntrusted: true,
			},
			msg: NormalizedMessage{
				Text:  "check this out",
				Links: []string{"https://example.com"},
			},
			trusted:    true,
			wantSignal: domain.Signal{},
			wantHit:    false,
		},
		{
			name: "link policy when disabled",
			rules: Rules{
				BlockLinksForUntrusted: false,
			},
			msg: NormalizedMessage{
				Text:  "check this out",
				Links: []string{"https://example.com"},
			},
			trusted:    false,
			wantSignal: domain.Signal{},
			wantHit:    false,
		},

		// Banned domain tests
		{
			name: "banned domain with https",
			rules: Rules{
				BannedDomains: []string{"badsite.com"},
			},
			msg: NormalizedMessage{
				Text:  "check this",
				Links: []string{"https://badsite.com/page"},
			},
			trusted:    true,
			wantSignal: domain.Signal{Name: "banned_domain", Detail: "badsite.com"},
			wantHit:    true,
		},
		{
			name: "banned domain case insensitive",
			rules: Rules{
				BannedDomains: []string{"BadSite.Com"},
			},
			msg: NormalizedMessage{
				Text:  "check this",
				Links: []string{"https://badsite.com/page"},
			},
			trusted:    true,
			wantSignal: domain.Signal{Name: "banned_domain", Detail: "badsite.com"},
			wantHit:    true,
		},
		{
			name: "banned domain with t.me link",
			rules: Rules{
				BannedDomains: []string{"t.me"},
			},
			msg: NormalizedMessage{
				Text:  "check this",
				Links: []string{"t.me/mychannel"},
			},
			trusted:    true,
			wantSignal: domain.Signal{Name: "banned_domain", Detail: "t.me"},
			wantHit:    true,
		},
		{
			name: "not banned domain",
			rules: Rules{
				BannedDomains: []string{"badsite.com"},
			},
			msg: NormalizedMessage{
				Text:  "check this",
				Links: []string{"https://goodsite.com/page"},
			},
			trusted:    true,
			wantSignal: domain.Signal{},
			wantHit:    false,
		},

		// Deterministic order: deny before link policy before banned domain
		{
			name: "deny takes precedence over link policy",
			rules: Rules{
				DenyStopwords:          []string{"crypto"},
				BlockLinksForUntrusted: true,
			},
			msg: NormalizedMessage{
				Text:  "buy crypto",
				Links: []string{"https://example.com"},
			},
			trusted:    false,
			wantSignal: domain.Signal{Name: "deny_stopword", Detail: "crypto"},
			wantHit:    true,
		},
		{
			name: "link policy takes precedence over banned domain",
			rules: Rules{
				BlockLinksForUntrusted: true,
				BannedDomains:          []string{"badsite.com"},
			},
			msg: NormalizedMessage{
				Text:  "check this",
				Links: []string{"https://badsite.com/page"},
			},
			trusted: false,
			// Host only: the signal is persisted in the audit table, and a
			// path/query would keep an observed user's invite or referral
			// codes there forever.
			wantSignal: domain.Signal{Name: "link_from_untrusted", Detail: "badsite.com"},
			wantHit:    true,
		},

		// No match
		{
			name:       "no rules match",
			rules:      Rules{},
			msg:        NormalizedMessage{Text: "hello world"},
			trusted:    false,
			wantSignal: domain.Signal{},
			wantHit:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, hit := tt.rules.Check(tt.msg, tt.trusted)
			if hit != tt.wantHit {
				t.Errorf("got hit=%v, want %v", hit, tt.wantHit)
			}
			if sig != tt.wantSignal {
				t.Errorf("got signal=%v, want %v", sig, tt.wantSignal)
			}
		})
	}
}

// TestCyrillicStopwordMatchesDeobfuscatedText covers the reason the whole
// deny/allow list was inert in Russian: Normalize folds the Cyrillic
// letters that look Latin, so the message reads "paбota" by the time the
// rule runs, while the configured stopword is still plain "работа".
func TestCyrillicStopwordMatchesDeobfuscatedText(t *testing.T) {
	r := Rules{DenyStopwords: []string{"работа"}}
	n := Normalize(domain.Message{Text: "Требуется РАБОТА на дому"})
	sig, hit := r.Check(n, false)
	if !hit || sig.Name != "deny_stopword" {
		t.Fatalf("got %v hit=%v, want a deny_stopword hit", sig, hit)
	}

	// And the allow list must override it through the same folding.
	r.AllowStopwords = []string{"на дому"}
	if _, hit := r.Check(n, false); hit {
		t.Fatal("allow stopword must override the deny stopword")
	}
}

// TestExtractHostDropsEverythingButTheHost: the result is persisted in the
// audit row, so a query string (invite codes, referral ids, session tokens)
// or a port must never survive into it.
func TestExtractHostDropsEverythingButTheHost(t *testing.T) {
	cases := map[string]string{
		"https://bad.com/path?invite=secret#frag": "bad.com",
		"bad.com?invite=secret":                   "bad.com",
		"bad.com/path#frag":                       "bad.com",
		"t.me/channel/123":                        "t.me",
		"https://bad.com:8443/x":                  "bad.com",
		"bad.com":                                 "bad.com",
	}
	for in, want := range cases {
		if got := extractHost(in); got != want {
			t.Errorf("extractHost(%q) = %q, want %q", in, got, want)
		}
	}
}
