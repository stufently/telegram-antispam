package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestNormalizeLinkOccurrences(t *testing.T) {
	tests := []struct {
		name    string
		message domain.Message
		want    int
	}{
		{"telegram URL regex overlap", domain.Message{Text: "https://t.me/example"}, 1},
		{"repeated URLs remain separate", domain.Message{Text: "https://t.me/example https://t.me/example"}, 2},
		{"hidden links with same destination", domain.Message{Text: "one two", Entities: []domain.Entity{
			{Type: "text_link", Offset: 0, Length: 3, URL: "https://example.org"},
			{Type: "text_link", Offset: 4, Length: 3, URL: "https://example.org"},
		}}, 2},
		{"adjacent hidden links remain separate", domain.Message{Text: "onetwo", Entities: []domain.Entity{
			{Type: "text_link", Offset: 0, Length: 3, URL: "https://example.org"},
			{Type: "text_link", Offset: 3, Length: 3, URL: "https://example.org"},
		}}, 2},
		{"bare URL entity", domain.Message{Text: "example.org", Entities: []domain.Entity{{Type: "url", Length: 11}}}, 1},
		{"entity and both regexes overlap", domain.Message{Text: "https://t.me/x", Entities: []domain.Entity{{Type: "url", Length: 13}}}, 1},
		{"hidden link label looks like URL", domain.Message{Text: "https://t.me/x", Entities: []domain.Entity{{Type: "text_link", Length: 13, URL: "https://example.org"}}}, 1},
		{"UTF16 offsets after emoji", domain.Message{Text: "😀 example.org https://t.me/x", Entities: []domain.Entity{
			{Type: "url", Offset: 3, Length: 11}, {Type: "url", Offset: 15, Length: 13},
		}}, 2},
		{"other surfaces count independently", domain.Message{Text: "https://t.me/x", ExternalReplyText: "https://t.me/x", PollOptionTexts: []string{"https://t.me/x", "https://t.me/x"}, SenderTag: "https://t.me/x"}, 5},
		{"invalid entities ignored", domain.Message{Text: "😀 abc", Entities: []domain.Entity{
			{Type: "url", Offset: -1, Length: 3}, {Type: "url", Offset: 1, Length: 1},
			{Type: "url", Offset: 3, Length: 100}, {Type: "text_link", Length: 0, URL: "https://example.org"},
			{Type: "text_link", Offset: 3, Length: 3},
		}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.message).LinkCount; got != tt.want {
				t.Fatalf("LinkCount = %d, want %d", got, tt.want)
			}
		})
	}
}
