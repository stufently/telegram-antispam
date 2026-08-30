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

func TestReportedAPKIsAHardMatchByDocumentType(t *testing.T) {
	r := Rules{BannedDocumentExtensions: []string{".apk"}}
	msg := domain.Message{
		Text:               "Спискк проппвших на С.ВО👆",
		MediaKinds:         []string{"document"},
		DocumentExtensions: []string{".apk"},
	}
	if sig, hit := r.Check(Normalize(msg), false); !hit || sig.Name != "banned_document_extension" {
		t.Fatalf("APK passed: hit=%v signal=%+v", hit, sig)
	}
}

// A banned attachment condemns its own message and no other. Replying to an
// .apk is exactly what the person warning "не ставьте, это вирус" does, and an
// automatic delete_mute on that reply would be a false ban with no text
// evidence behind it — so the reply parent must stay out of detection, and the
// context it carries goes only to the (advisory, fail-open) LLM stage.
func TestReplyToABannedDocumentIsNotItselfAHardMatch(t *testing.T) {
	r := Rules{
		BannedDocumentExtensions: []string{".apk"},
		BannedDocumentMIMETypes:  []string{"application/vnd.android.package-archive"},
	}
	msg := domain.Message{
		Text: "не ставьте это, вирус",
		ReplyTo: &domain.Message{
			MediaKinds:         []string{"document"},
			DocumentExtensions: []string{".apk"},
			DocumentMIMETypes:  []string{"application/vnd.android.package-archive"},
		},
	}
	if sig, hit := r.Check(Normalize(msg), false); hit {
		t.Fatalf("the reply carries no attachment of its own, got signal %+v", sig)
	}
}

// Same rule for the cross-chat form of a reply, which arrives on different
// fields entirely (external_reply, not reply_to_message) and so could easily
// have been wired into detection while the in-chat one stayed out. Warning
// people off a file posted in some channel is, if anything, MORE ordinary than
// warning them off one posted here.
func TestExternalReplyToABannedDocumentIsNotItselfAHardMatch(t *testing.T) {
	r := Rules{
		BannedDocumentExtensions: []string{".apk"},
		BannedDocumentMIMETypes:  []string{"application/vnd.android.package-archive"},
	}
	msg := domain.Message{
		Text:                            "не ставьте это, вирус",
		ExternalReplyMediaKinds:         []string{"document"},
		ExternalReplyDocumentExtensions: []string{".apk"},
		ExternalReplyDocumentMIMETypes:  []string{"application/vnd.android.package-archive"},
	}
	n := Normalize(msg)
	if len(n.MediaKinds) != 0 || len(n.DocumentExtensions) != 0 || len(n.DocumentMIMETypes) != 0 {
		t.Fatalf("the external reply's attachment reached normalization: %+v", n)
	}
	if sig, hit := r.Check(n, false); hit {
		t.Fatalf("the reply carries no attachment of its own, got signal %+v", sig)
	}
}
