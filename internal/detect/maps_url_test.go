package detect

import (
	"testing"
)

func TestAllowedGoogleMapsURL(t *testing.T) {
	allow := []string{
		"https://maps.app.goo.gl/AbCd",
		"http://maps.app.goo.gl/AbCd",
		"maps.app.goo.gl/AbCd",
		"http://maps.google.com/",
		"https://maps.google.com/",
		"https://maps.google.com",
		"https://maps.google.com/?q=x",
		"https://maps.google.com/maps",
		"https://maps.google.com/maps/place/x",
		"https://maps.google.com/maps/@1,2,3z",
		"https://google.com/maps",
		"https://google.com/maps/place/x",
		"https://google.com/maps/@1,2,3z",
		"https://www.google.com/maps?q=bangkok",
		"https://www.google.com/maps/dir/a/b",
		"https://Google.COM/maps",
		"https://WWW.GOOGLE.COM/maps/place/x",
		"google.com/maps",
		"www.google.com/maps/place/x",
	}
	for _, link := range allow {
		if !allowedGoogleMapsURL(link) {
			t.Errorf("allowedGoogleMapsURL(%q) = false, want true", link)
		}
	}

	reject := []string{
		"https://google.com/MAPS",
		"https://maps.google.com/MAPS",
		"https://maps.google.com/url",
		"https://google.com/url",
		"https://google.com/maps-evil",
		"https://google.com/mapsfoo",
		"https://google.com/maps/../url",
		"https://google.com/maps/../search",
		"https://google.com/notmaps/../maps",
		"https://google.com/maps/%2e%2e/url",
		"https://google.com/maps/%2e%2e%2furl",
		"https://google.com/",
		"https://google.com",
		"https://www.google.com/",
		"https://www.google.com/search?q=maps",
		"https://evil.com@maps.google.com/maps",
		"https://maps.google.com@evil.com/",
		"https://maps.google.com:8443/maps",
		"https://maps.google.com:443/maps",
		"ftp://maps.google.com/maps",
		"javascript:https://maps.google.com/maps",
		"tg://maps.google.com",
		"data:text/html,https://maps.google.com/maps",
		"https://maps.google.com.evil.com/maps",
		"https://maps.google.com./maps",
		"https://goo.gl/maps/xyz",
		"https://www.maps.google.com/maps",
		"https://maps.google.co.th/maps",
		"https://google.co.th/maps",
		"https://google.ru/maps",
		"https://drive.google.com/",
		"https://maps.app.goo.gl/",
		"https://maps.app.goo.gl",
		"maps.app.goo.gl",
		"https://maps.app.goo.gl/../evil",
		"https://xn--mpas-0na.google.com/maps",
		"https://марѕ.gооglе.соm/maps",
		"https://maps.goo\u200bgle.com/maps",
		"https://ｍａｐｓ.ｇｏｏｇｌｅ.ｃｏｍ/maps",
		"https://127.0.0.1/maps",
		"",
		"not a url",
	}
	for _, link := range reject {
		if allowedGoogleMapsURL(link) {
			t.Errorf("allowedGoogleMapsURL(%q) = true, want false", link)
		}
	}
}

func TestAllowedGoogleMapsURLDoesNotFoldOriginalBytes(t *testing.T) {
	// Links are collected before Deobfuscate. Folding here would turn a
	// lookalike DNS name into maps.google.com and exempt it.
	folded := Deobfuscate("https://марѕ.gооglе.соm/maps")
	if folded != "https://maps.google.com/maps" {
		t.Fatalf("fixture no longer folds to ASCII maps: %q", folded)
	}
	if allowedGoogleMapsURL("https://марѕ.gооglе.соm/maps") {
		t.Fatal("homograph host must not match after any fold")
	}
}
