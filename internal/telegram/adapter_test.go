package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestToDomainMessagePlainUser(t *testing.T) {
	m := &models.Message{
		ID:   55,
		Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 7, Username: "bob", FirstName: "Bob"},
		Text: "hello",
		Date: 1700,
	}
	got := ToDomainMessage(m)
	if got.ChatID != -100123 || got.MessageID != 55 || got.Text != "hello" {
		t.Fatalf("bad envelope: %+v", got)
	}
	if got.Sender.Kind != domain.SenderUser || got.Sender.UserID != 7 || got.Sender.Username != "bob" {
		t.Fatalf("bad sender: %+v", got.Sender)
	}
}

func TestToDomainMessageExternalChannel(t *testing.T) {
	m := &models.Message{
		ID:         9,
		Chat:       models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		SenderChat: &models.Chat{ID: -100888, Type: models.ChatTypeChannel},
	}
	got := ToDomainMessage(m)
	if got.Sender.Kind != domain.SenderExternalChannel || got.Sender.SenderChatID != -100888 {
		t.Fatalf("bad sender: %+v", got.Sender)
	}
}
