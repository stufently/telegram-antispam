package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func contains(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

func containsSubstr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestNormalizeCollectsLinksAndText(t *testing.T) {
	m := domain.Message{
		Text:            "join now",
		Entities:        []domain.Entity{{Type: "text_link", URL: "http://spam.test", Offset: 0, Length: 4}},
		PollOptionTexts: []string{"vote t.me/scam"},
	}
	n := Normalize(m)
	if !contains(n.Links, "http://spam.test") {
		t.Fatalf("hidden text_link not collected: %v", n.Links)
	}
	if !containsSubstr(n.Text, "join now") {
		t.Fatalf("text not normalized in: %q", n.Text)
	}
}

func TestNormalizeCollectsHiddenAndPlainLinks(t *testing.T) {
	m := domain.Message{
		Text:              "click here",
		Entities:          []domain.Entity{{Type: "text_link", URL: "https://hidden.example/x", Offset: 0, Length: 5}},
		ExternalReplyText: "see t.me/plainchannel",
	}
	n := Normalize(m)
	if !contains(n.Links, "https://hidden.example/x") {
		t.Fatalf("hidden text_link URL not collected: %v", n.Links)
	}
	if !contains(n.Links, "t.me/plainchannel") {
		t.Fatalf("plain t.me/ token not collected: %v", n.Links)
	}
}

func TestNormalizeHasCustomEmoji(t *testing.T) {
	m := domain.Message{
		Text:     "hi",
		Entities: []domain.Entity{{Type: "custom_emoji", Offset: 0, Length: 2}},
	}
	n := Normalize(m)
	if !n.HasCustomEmoji {
		t.Fatalf("expected HasCustomEmoji=true, got false")
	}

	m2 := domain.Message{Text: "hi"}
	n2 := Normalize(m2)
	if n2.HasCustomEmoji {
		t.Fatalf("expected HasCustomEmoji=false, got true")
	}
}

func TestNormalizeCollectsMentionsAndRawLen(t *testing.T) {
	m := domain.Message{
		Text: "hey @spammer check this",
	}
	n := Normalize(m)
	if !contains(n.Mentions, "@spammer") {
		t.Fatalf("mention not collected: %v", n.Mentions)
	}
	wantLen := len([]rune(m.Text))
	if n.RawLen != wantLen {
		t.Fatalf("RawLen = %d, want %d", n.RawLen, wantLen)
	}
}

func TestNormalizeDedupsLinksAndMentions(t *testing.T) {
	m := domain.Message{
		Text:     "http://dup.test http://dup.test @same @same",
		Entities: []domain.Entity{{Type: "text_link", URL: "http://dup.test", Offset: 0, Length: 4}},
	}
	n := Normalize(m)
	count := 0
	for _, l := range n.Links {
		if l == "http://dup.test" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected http://dup.test once, got %d: %v", count, n.Links)
	}
	mcount := 0
	for _, mm := range n.Mentions {
		if mm == "@same" {
			mcount++
		}
	}
	if mcount != 1 {
		t.Fatalf("expected @same once, got %d: %v", mcount, n.Mentions)
	}
}

// TestCollectLinksBareDomainEntity covers the shape a spammer actually
// types: a link with no scheme. Telegram marks it as a "url" entity with an
// empty URL field and leaves the address in the text, where the http(s)
// regex never sees it.
func TestCollectLinksBareDomainEntity(t *testing.T) {
	m := domain.Message{
		Text: "пиши сюда bit.ly/abc быстро",
		Entities: []domain.Entity{
			{Type: "url", Offset: 10, Length: 10},
		},
	}
	got := Normalize(m).Links
	if len(got) != 1 || got[0] != "bit.ly/abc" {
		t.Fatalf("links = %v, want [bit.ly/abc]", got)
	}
}

// TestCollectLinksEntityOffsetsAreUTF16 pins the unit the Bot API counts in.
// An emoji before the link is one rune in Go but TWO UTF-16 units, so a
// rune- or byte-based slice would cut the address short.
func TestCollectLinksEntityOffsetsAreUTF16(t *testing.T) {
	m := domain.Message{
		Text: "🔥 example.org",
		Entities: []domain.Entity{
			{Type: "url", Offset: 3, Length: 11},
		},
	}
	got := Normalize(m).Links
	if len(got) != 1 || got[0] != "example.org" {
		t.Fatalf("links = %v, want [example.org]", got)
	}
}

// TestEntitySpanOutOfRangeIsEmpty: a malformed entity must not panic the
// process — one bad update would take all ten chats down with it.
func TestEntitySpanOutOfRangeIsEmpty(t *testing.T) {
	for _, tc := range []struct{ off, length int }{{100, 5}, {-1, 5}, {0, 0}, {2, 100}} {
		if got := entitySpan("short", tc.off, tc.length); got != "" {
			t.Errorf("entitySpan(%d,%d) = %q, want empty", tc.off, tc.length, got)
		}
	}
}
