package detect

import (
	"strings"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// Messages whose normalized blob is non-empty only because of text the
// sender did not write for this message (a sender tag, a quoted external
// reply) must stay out of the duplicate and short-message windows: every
// such message produces the same blob, so a run of captionless photos reads
// as a duplicate burst.
func TestBehaviorSkipsMessagesWithoutOwnText(t *testing.T) {
	cases := map[string]domain.Message{
		"captionless photo with sender tag": {SenderTag: "promo", MediaKinds: []string{"photo"}},
		"sticker under an external quote":   {ExternalReplyText: "quoted channel post", MediaKinds: []string{"sticker"}},
	}
	cfg := BehaviorCfg{
		DupThreshold: 3, DupWindow: time.Hour,
		ShortFloodThreshold: 3, ShortLen: 10, ShortWindow: time.Minute,
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			n := Normalize(m)
			if n.Text == "" {
				t.Fatalf("precondition: normalized text is empty, the case would not exercise the own-text gate")
			}
			if n.HasOwnText {
				t.Fatalf("HasOwnText = true for a message without authored text")
			}
			h := NewMemHistory()
			for i := 0; i < 5; i++ {
				if sig, hit := CheckBehavior(h, 1, 2, n, false, false, cfg); hit {
					t.Fatalf("message %d triggered %q", i, sig.Name)
				}
			}
			counter := &shortCounter{fakeHistory: fakeHistory{shortCounts: 99, defaultDupCount: 99}}
			ObserveBehavior(counter, 1, 2, n, cfg)
			CheckBehavior(counter, 1, 2, n, false, false, cfg)
			if len(counter.recordedDups) != 0 || counter.shortCalls != 0 {
				t.Fatalf("recorded into windows: dup=%d short=%d", len(counter.recordedDups), counter.shortCalls)
			}
		})
	}
}

// The gate must not open a hole for real repeats: the same caption under the
// same tag is still a duplicate, and a poll the sender wrote is own text.
func TestBehaviorStillCountsOwnText(t *testing.T) {
	cfg := BehaviorCfg{DupThreshold: 3, DupWindow: time.Hour}
	cases := map[string]domain.Message{
		"caption with sender tag": {Text: "buy now", SenderTag: "promo", MediaKinds: []string{"photo"}},
		"poll without text":       {StructuredTexts: []domain.TextPart{{Text: "best casino?"}, {Text: "join t.me/x"}}},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			n := Normalize(m)
			if !n.HasOwnText {
				t.Fatalf("HasOwnText = false")
			}
			h := NewMemHistory()
			var sig domain.Signal
			var hit bool
			for i := 0; i < 3; i++ {
				sig, hit = CheckBehavior(h, 1, 2, n, false, false, cfg)
			}
			if !hit || sig.Name != "duplicate_flood" {
				t.Fatalf("third repeat: hit=%v sig=%q, want duplicate_flood", hit, sig.Name)
			}
		})
	}
}

func TestHasOwnTextIgnoresWhitespace(t *testing.T) {
	n := Normalize(domain.Message{Text: "  \n", StructuredTexts: []domain.TextPart{{Text: " "}}, SenderTag: "tag"})
	if n.HasOwnText {
		t.Fatal("whitespace-only text counted as own text")
	}
}

// A checklist or poll without message text reaches the link detectors.
func TestNormalizeReadsStructuredTextLinks(t *testing.T) {
	n := Normalize(domain.Message{StructuredTexts: []domain.TextPart{{Text: "todo"}, {Text: "join t.me/scam"}}})
	if !contains(n.Links, "t.me/scam") && !containsSubstr(n.Text, "t.me/scam") {
		t.Fatalf("checklist link lost: links=%v text=%q", n.Links, n.Text)
	}
	if n.LinkCount != 1 {
		t.Fatalf("LinkCount = %d, want 1", n.LinkCount)
	}
}

// Poll and checklist pieces keep their own entities: a scheme-less url span
// is a link, a text_link URL keeps its exact bytes, and a visible URL with its
// own url entity is one occurrence.
func TestNormalizeStructuredEntities(t *testing.T) {
	n := Normalize(domain.Message{StructuredTexts: []domain.TextPart{
		{Text: "go bit.ly/x now", Entities: []domain.Entity{{Type: "url", Offset: 3, Length: 8}}},
		{Text: "map", Entities: []domain.Entity{{Type: "text_link", URL: "https://google.com/maps.", Offset: 0, Length: 3}}},
		{Text: "see https://a.example/p", Entities: []domain.Entity{{Type: "url", Offset: 4, Length: 19}}},
	}})
	for _, want := range []string{"bit.ly/x", "https://google.com/maps.", "https://a.example/p"} {
		if !contains(n.Links, want) {
			t.Errorf("Links %v lack %q", n.Links, want)
		}
	}
	if n.LinkCount != 3 {
		t.Errorf("LinkCount = %d, want 3", n.LinkCount)
	}
}

// Short-message flood measures the sender's own text, poll and checklist
// pieces included: a long checklist is not a short message.
func TestShortFloodMeasuresOwnLength(t *testing.T) {
	cfg := BehaviorCfg{ShortFloodThreshold: 3, ShortLen: 10, ShortWindow: time.Minute}
	long := Normalize(domain.Message{StructuredTexts: []domain.TextPart{{Text: "shopping list for the weekend"}, {Text: "buy bread and milk"}}})
	if long.OwnLen <= cfg.ShortLen {
		t.Fatalf("precondition: OwnLen = %d", long.OwnLen)
	}
	h := NewMemHistory()
	for i := 0; i < 5; i++ {
		if sig, hit := CheckBehavior(h, 1, 2, long, false, false, cfg); hit {
			t.Fatalf("long checklist %d triggered %q", i, sig.Name)
		}
	}
	short := Normalize(domain.Message{StructuredTexts: []domain.TextPart{{Text: "ok"}}})
	h = NewMemHistory()
	var hit bool
	for i := 0; i < 4; i++ {
		_, hit = CheckBehavior(h, 1, 2, short, false, false, cfg)
	}
	if !hit {
		t.Fatal("short poll burst did not trigger short_flood")
	}
}

// Bayes length features and the emoji rule read poll and checklist pieces too.
func TestStructuredTextLengthAndEmoji(t *testing.T) {
	long := strings.Repeat("a", 210)
	n := Normalize(domain.Message{StructuredTexts: []domain.TextPart{
		{Text: long},
		{Text: "x", Entities: []domain.Entity{{Type: "custom_emoji", Offset: 0, Length: 1}}},
	}})
	toks := Tokenize(n)
	if !contains(toks, "len:long") || contains(toks, "len:short") {
		t.Fatalf("length tokens wrong for a long poll: %v", toks)
	}
	if !n.HasCustomEmoji || n.EmojiCount != 1 {
		t.Fatalf("custom emoji in a poll piece: has=%v count=%d", n.HasCustomEmoji, n.EmojiCount)
	}
}
