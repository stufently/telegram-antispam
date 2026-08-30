package main

import (
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestLLMMessageTextPrefixesStructuralFacts(t *testing.T) {
	got := llmMessageText(domain.Message{
		Text:               "подробности в лс",
		MediaKinds:         []string{"photo"},
		DocumentExtensions: []string{".apk"},
		DocumentMIMETypes:  []string{"application/vnd.android.package-archive"},
		Forwarded:          true,
		ForwardedFromChat:  true,
		HasKeyboard:        true,
	})
	const want = "[метаданные сообщения: вложение: photo; расширения документов: .apk; " +
		"MIME документов: application/vnd.android.package-archive; переслано из канала или группы; " +
		"кнопки под сообщением: такое может прислать только бот]\nподробности в лс"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestLLMMessageTextLeavesPlainTextAlone(t *testing.T) {
	if got := llmMessageText(domain.Message{Text: "привет"}); got != "привет" {
		t.Fatalf("a message with nothing structural to say must be sent verbatim, got %q", got)
	}
}

func TestLLMMessageTextNamesViaBotAsTheInnocentCase(t *testing.T) {
	got := llmMessageText(domain.Message{Text: "x", HasKeyboard: true, ViaBot: true})
	if !strings.Contains(got, "через инлайн-бота") {
		t.Fatalf("via_bot must be stated, not hidden behind the bot accusation: %q", got)
	}
}

// The production miss of 2026-08-29: the .apk travelled alone and a separate
// short reply sold it, so the judged message had no structural facts of its
// own and the model saw three innocuous words.
func TestLLMMessageTextDescribesTheAttachmentItRepliesTo(t *testing.T) {
	got := llmMessageText(domain.Message{
		Text: "Обновили наконец !",
		ReplyTo: &domain.Message{
			Text:               "тут файл",
			MediaKinds:         []string{"document"},
			DocumentExtensions: []string{".apk"},
		},
	})
	const want = "[сообщение, на которое отвечают: вложение: document; расширения документов: .apk]\n" +
		"Обновили наконец !"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestLLMMessageTextKeepsTheParentTextInside(t *testing.T) {
	got := llmMessageText(domain.Message{
		Text: "ага",
		ReplyTo: &domain.Message{
			Text:       "мой телефон +66 812345678, пишите",
			MediaKinds: []string{"document"},
		},
	})
	if strings.Contains(got, "телефон") {
		t.Fatalf("another person's words must not leave the process, only the attachment type: %q", got)
	}
}

func TestLLMMessageTextIgnoresAPlainReplyParent(t *testing.T) {
	got := llmMessageText(domain.Message{
		Text:    "согласен",
		ReplyTo: &domain.Message{Text: "погода сегодня отличная"},
	})
	if got != "согласен" {
		t.Fatalf("a parent with nothing structural to say must add no line at all, got %q", got)
	}
}

func TestLLMMessageTextKeepsBothLinesSeparate(t *testing.T) {
	got := llmMessageText(domain.Message{
		Text:       "смотрите",
		MediaKinds: []string{"photo"},
		ReplyTo: &domain.Message{
			MediaKinds:         []string{"document"},
			DocumentMIMETypes:  []string{"application/vnd.android.package-archive"},
			DocumentExtensions: []string{".apk"},
		},
	})
	const want = "[метаданные сообщения: вложение: photo]\n" +
		"[сообщение, на которое отвечают: вложение: document; расширения документов: .apk; " +
		"MIME документов: application/vnd.android.package-archive]\n" +
		"смотрите"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}
