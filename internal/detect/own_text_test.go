package detect

import (
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
		"poll without text":       {StructuredTexts: []string{"best casino?", "join t.me/x"}},
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
	n := Normalize(domain.Message{Text: "  \n", StructuredTexts: []string{" "}, SenderTag: "tag"})
	if n.HasOwnText {
		t.Fatal("whitespace-only text counted as own text")
	}
}

// A checklist or poll without message text reaches the link detectors.
func TestNormalizeReadsStructuredTextLinks(t *testing.T) {
	n := Normalize(domain.Message{StructuredTexts: []string{"todo", "join t.me/scam"}})
	if !contains(n.Links, "t.me/scam") && !containsSubstr(n.Text, "t.me/scam") {
		t.Fatalf("checklist link lost: links=%v text=%q", n.Links, n.Text)
	}
	if n.LinkCount != 1 {
		t.Fatalf("LinkCount = %d, want 1", n.LinkCount)
	}
}
