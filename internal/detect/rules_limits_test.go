package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func limitMsg(text string) domain.Message {
	return domain.Message{ChatID: -100, MessageID: 1, Text: text,
		Sender: domain.Sender{Kind: domain.SenderUser, UserID: 7}}
}

func TestDenyExactMatchesOnlyTheWholeMessage(t *testing.T) {
	r := Rules{DenyExact: []string{"работа"}}

	if _, hit := r.Check(Normalize(limitMsg("работа")), false); !hit {
		t.Fatal("the one-word bait must match")
	}
	if _, hit := r.Check(Normalize(limitMsg("работаю с девяти утра")), false); hit {
		t.Fatal("an ordinary sentence containing the word must NOT match — that is the whole point of deny_exact")
	}
}

func TestDenyExactHonorsTheAllowList(t *testing.T) {
	r := Rules{DenyExact: []string{"работа"}, AllowStopwords: []string{"работа"}}
	if _, hit := r.Check(Normalize(limitMsg("работа")), false); hit {
		t.Fatal("an allow entry means 'this text is fine here', however the deny side was written")
	}
}

func TestOccurrenceLimitsCountRepeats(t *testing.T) {
	r := Rules{MaxMentions: 2}
	// The deduplicated Mentions list would report one; the flood is five.
	sig, hit := r.Check(Normalize(limitMsg("@bob @bob @bob @bob @bob")), false)
	if !hit || sig.Name != "too_many_mentions" {
		t.Fatalf("hit=%v signal=%+v, want a mention flood", hit, sig)
	}
	if _, hit := r.Check(Normalize(limitMsg("@bob @alice")), false); hit {
		t.Fatal("two mentions are at the limit, not over it")
	}
}

func TestOccurrenceLimitsExemptTrustedMembers(t *testing.T) {
	r := Rules{MaxLinks: 1}
	msg := Normalize(limitMsg("https://a.example https://b.example"))
	if _, hit := r.Check(msg, false); !hit {
		t.Fatal("a newcomer posting two links must trip the limit")
	}
	if _, hit := r.Check(msg, true); hit {
		t.Fatal("a trusted member sharing links is sharing, not flooding")
	}
}

func TestEmojiLimitCountsEmojiRunes(t *testing.T) {
	r := Rules{MaxEmoji: 2}
	if _, hit := r.Check(Normalize(limitMsg("супер 🎉🎉🎉🎉")), false); !hit {
		t.Fatal("four emoji must trip a limit of two")
	}
	if _, hit := r.Check(Normalize(limitMsg("супер 🎉")), false); hit {
		t.Fatal("one emoji is ordinary chat")
	}
}

func TestLimitsAreOffByDefault(t *testing.T) {
	var r Rules
	if _, hit := r.Check(Normalize(limitMsg("@a @b @c @d 🎉🎉🎉🎉 https://x https://y")), false); hit {
		t.Fatal("with no limits configured nothing may fire")
	}
}

func TestReportedAPKLoanAndEuphemisticJobMessagesAreHardMatches(t *testing.T) {
	r := Rules{
		BannedDocumentExtensions: []string{".apk"},
		DenyExact: []string{
			"Дам в долг",
			"Словить карася вручную, зп договорная",
		},
	}
	tests := []domain.Message{
		{Text: "Спискк проппвших на С.ВО👆", MediaKinds: []string{"document"}, DocumentExtensions: []string{".apk"}},
		{Text: "Дам в долг"},
		{Text: "Словить карася вручную, зп договорная"},
	}
	for _, msg := range tests {
		if sig, hit := r.Check(Normalize(msg), false); !hit {
			t.Errorf("message %q passed, signal=%+v", msg.Text, sig)
		}
	}
}
