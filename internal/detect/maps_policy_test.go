package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestLinkPolicyGoogleMapsException(t *testing.T) {
	maps := NormalizedMessage{
		Text:  "see you there",
		Links: []string{"https://maps.google.com/maps"},
	}
	enabled := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	disabled := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: false}
	unset := Rules{BlockLinksForUntrusted: true}

	if sig, hit := unset.Check(maps, false); !hit || sig.Name != "link_from_untrusted" {
		t.Fatalf("unset flag must keep blocking Maps, got hit=%v sig=%+v", hit, sig)
	}
	if sig, hit := disabled.Check(maps, false); !hit || sig.Name != "link_from_untrusted" {
		t.Fatalf("disabled flag must keep blocking Maps, got hit=%v sig=%+v", hit, sig)
	}
	if sig, hit := enabled.Check(maps, false); hit {
		t.Fatalf("enabled Maps exception must skip link_from_untrusted, got %+v", sig)
	}
}

func TestLinkPolicyGoogleMapsRequiresEveryLink(t *testing.T) {
	r := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	mapsThenEvil := NormalizedMessage{
		Links: []string{"https://maps.google.com/maps", "https://evil.example/x"},
	}
	evilThenMaps := NormalizedMessage{
		Links: []string{"https://evil.example/x", "https://maps.google.com/maps"},
	}

	sig, hit := r.Check(mapsThenEvil, false)
	if !hit || sig.Name != "link_from_untrusted" || sig.Detail != "evil.example" {
		t.Fatalf("maps+evil: got hit=%v sig=%+v, want first nonexempt host evil.example", hit, sig)
	}
	sig, hit = r.Check(evilThenMaps, false)
	if !hit || sig.Name != "link_from_untrusted" || sig.Detail != "evil.example" {
		t.Fatalf("evil+maps: got hit=%v sig=%+v, want first nonexempt host evil.example", hit, sig)
	}
}

func TestLinkPolicyGoogleMapsOtherRulesStillFire(t *testing.T) {
	maps := NormalizedMessage{
		Text:  "crypto meetup https://maps.google.com/maps",
		Links: []string{"https://maps.google.com/maps"},
	}
	deny := Rules{
		BlockLinksForUntrusted: true,
		AllowGoogleMapsLinks:   true,
		DenyStopwords:          []string{"crypto"},
	}
	sig, hit := deny.Check(maps, false)
	if !hit || sig.Name != "deny_stopword" {
		t.Fatalf("deny stopword must still win, got hit=%v sig=%+v", hit, sig)
	}

	banned := Rules{
		BlockLinksForUntrusted: true,
		AllowGoogleMapsLinks:   true,
		BannedDomains:          []string{"google.com"},
	}
	googleMaps := NormalizedMessage{Links: []string{"https://google.com/maps"}}
	sig, hit = banned.Check(googleMaps, false)
	if !hit || sig.Name != "banned_domain" || sig.Detail != "google.com" {
		t.Fatalf("banned domain must still fire, got hit=%v sig=%+v", hit, sig)
	}

	limited := Rules{
		BlockLinksForUntrusted: true,
		AllowGoogleMapsLinks:   true,
		MaxLinks:               1,
	}
	two := NormalizedMessage{
		Links:     []string{"https://maps.google.com/maps", "https://maps.app.goo.gl/AbCd"},
		LinkCount: 2,
	}
	sig, hit = limited.Check(two, false)
	if !hit || sig.Name != "too_many_links" {
		t.Fatalf("occurrence limit must still fire, got hit=%v sig=%+v", hit, sig)
	}
}

func TestLinkPolicyGoogleMapsHiddenEntityAndCaption(t *testing.T) {
	r := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	hidden := Normalize(domain.Message{
		Text:     "here",
		Entities: []domain.Entity{{Type: "text_link", URL: "https://www.google.com/maps?q=bangkok", Offset: 0, Length: 4}},
	})
	if sig, hit := r.Check(hidden, false); hit {
		t.Fatalf("hidden Maps text_link must be exempt, got %+v", sig)
	}

	caption := Normalize(domain.Message{Text: "meet https://maps.app.goo.gl/AbCd"})
	if sig, hit := r.Check(caption, false); hit {
		t.Fatalf("caption Maps URL must be exempt, got %+v", sig)
	}

	schemeless := Normalize(domain.Message{
		Text:     "maps.app.goo.gl/AbCd",
		Entities: []domain.Entity{{Type: "url", Offset: 0, Length: 20}},
	})
	if !contains(schemeless.Links, "maps.app.goo.gl/AbCd") {
		t.Fatalf("scheme-less url entity was not collected: %v", schemeless.Links)
	}
	if sig, hit := r.Check(schemeless, false); hit {
		t.Fatalf("scheme-less url entity must be exempt, got %+v links=%v", sig, schemeless.Links)
	}
}

func TestLinkPolicyGoogleMapsRejectsSpoofs(t *testing.T) {
	r := Rules{BlockLinksForUntrusted: true, AllowGoogleMapsLinks: true}
	spoofs := []string{
		"https://evil.com@maps.google.com/maps",
		"https://maps.google.com:8443/maps",
		"https://google.com/maps-evil",
		"https://google.com/MAPS",
		"https://maps.google.com.evil.com/maps",
		"ftp://maps.google.com/maps",
	}
	for _, link := range spoofs {
		sig, hit := r.Check(NormalizedMessage{Links: []string{link}}, false)
		if !hit || sig.Name != "link_from_untrusted" {
			t.Errorf("%q: want link_from_untrusted, got hit=%v sig=%+v", link, hit, sig)
		}
	}
}

func TestTokenizeStillSeesOriginalMapsHost(t *testing.T) {
	n := NormalizedMessage{Links: []string{"https://maps.google.com/maps?q=x"}}
	toks := Tokenize(n)
	found := false
	for _, tok := range toks {
		if tok == "host:maps.google.com" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Bayes tokens must still include original host, got %v", toks)
	}
}
