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

func TestCollectLinksKeepsDotDotPathSegment(t *testing.T) {
	const raw = "https://google.com/maps/.."
	n := Normalize(domain.Message{Text: "see " + raw})
	if !contains(n.Links, raw) {
		t.Fatalf("dot-dot path must not be trimmed to /maps/: %v", n.Links)
	}
	hidden := Normalize(domain.Message{
		Text:     "here",
		Entities: []domain.Entity{{Type: "text_link", URL: raw, Offset: 0, Length: 4}},
	})
	if !contains(hidden.Links, raw) {
		t.Fatalf("text_link dot-dot path must be kept: %v", hidden.Links)
	}
	r := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	if sig, hit := r.Check(n, false); !hit || sig.Name != "link_from_untrusted" {
		t.Fatalf("trimmed-away .. would exempt google.com root, got hit=%v sig=%+v", hit, sig)
	}
}

func TestCollectLinksStillTrimsSentencePeriod(t *testing.T) {
	n := Normalize(domain.Message{Text: "see https://google.com/maps."})
	if !contains(n.Links, "https://google.com/maps") {
		t.Fatalf("sentence period must still be trimmed: %v", n.Links)
	}
}
