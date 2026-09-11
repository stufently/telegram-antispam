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

func TestCollectLinksKeepsAuthoritativeEntityPunctuation(t *testing.T) {
	r := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	for _, punct := range []string{".", ":", "!"} {
		raw := "https://google.com/maps" + punct
		hidden := Normalize(domain.Message{
			Text:     "here",
			Entities: []domain.Entity{{Type: "text_link", URL: raw, Offset: 0, Length: 4}},
		})
		if !contains(hidden.Links, raw) {
			t.Fatalf("text_link %q must keep original bytes, got %v", raw, hidden.Links)
		}
		if contains(hidden.Links, "https://google.com/maps") {
			t.Fatalf("text_link %q must not be rewritten to /maps, got %v", raw, hidden.Links)
		}
		if sig, hit := r.Check(hidden, false); !hit || sig.Name != "link_from_untrusted" {
			t.Fatalf("text_link %q must not be Maps-exempt, got hit=%v sig=%+v", raw, hit, sig)
		}

		span := Normalize(domain.Message{
			Text:     raw,
			Entities: []domain.Entity{{Type: "url", Offset: 0, Length: len(raw)}},
		})
		if !contains(span.Links, raw) {
			t.Fatalf("url entity %q must keep original bytes, got %v", raw, span.Links)
		}
		if sig, hit := r.Check(span, false); !hit || sig.Name != "link_from_untrusted" {
			t.Fatalf("url entity %q must not be Maps-exempt, got hit=%v sig=%+v", raw, hit, sig)
		}
	}
}

func TestCollectLinksPunctuationOutsideEntityStillMaps(t *testing.T) {
	r := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	const maps = "https://google.com/maps"
	hidden := Normalize(domain.Message{
		Text:     "here.",
		Entities: []domain.Entity{{Type: "text_link", URL: maps, Offset: 0, Length: 4}},
	})
	if !contains(hidden.Links, maps) {
		t.Fatalf("good Maps text_link must be kept: %v", hidden.Links)
	}
	if sig, hit := r.Check(hidden, false); hit {
		t.Fatalf("punctuation outside the entity must not poison a good Maps URL, got %+v", sig)
	}

	text := "see " + maps + "."
	span := Normalize(domain.Message{
		Text:     text,
		Entities: []domain.Entity{{Type: "url", Offset: 4, Length: len(maps)}},
	})
	if !contains(span.Links, maps) {
		t.Fatalf("url span excluding the period must keep %q, got %v", maps, span.Links)
	}
	if sig, hit := r.Check(span, false); hit {
		t.Fatalf("period outside url entity must still be Maps-exempt, got %+v links=%v", sig, span.Links)
	}
}

func TestCollectLinksRegexCannotOverrideNonexemptEntity(t *testing.T) {
	r := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	const entityURL = "https://google.com/maps."
	n := Normalize(domain.Message{
		Text:     "click https://google.com/maps",
		Entities: []domain.Entity{{Type: "text_link", URL: entityURL, Offset: 0, Length: 5}},
	})
	if !contains(n.Links, entityURL) {
		t.Fatalf("authoritative entity URL must stay in Links, got %v", n.Links)
	}
	if sig, hit := r.Check(n, false); !hit || sig.Name != "link_from_untrusted" {
		t.Fatalf("regex Maps candidate must not override a nonexempt entity, got hit=%v sig=%+v links=%v", hit, sig, n.Links)
	}
}
