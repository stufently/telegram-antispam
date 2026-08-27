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
