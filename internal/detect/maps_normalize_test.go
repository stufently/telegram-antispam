package detect

import (
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestNormalizeKeepsUnfoldedLookalikeHostsInLinks(t *testing.T) {
	cases := []string{
		"https://марѕ.gооglе.соm/maps",
		"https://maps.goo\u200bgle.com/maps",
		"https://ｍａｐｓ.ｇｏｏｇｌｅ.ｃｏｍ/maps",
	}
	for _, raw := range cases {
		n := Normalize(domain.Message{Text: "see " + raw})
		if !contains(n.Links, raw) {
			t.Fatalf("Links dropped or folded original URL %q: %v", raw, n.Links)
		}
		for _, link := range n.Links {
			if strings.Contains(link, "maps.google.com") && link != raw {
				t.Fatalf("Links folded %q into %q", raw, link)
			}
		}
		if allowedGoogleMapsURL(raw) {
			t.Fatalf("unfolded lookalike %q must not be exempt", raw)
		}
	}
}
