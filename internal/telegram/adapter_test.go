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

func TestToDomainMessageExtractsEntities(t *testing.T) {
	m := &models.Message{
		ID:   5, Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 7},
		Text: "click here",
		Entities: []models.MessageEntity{
			{Type: models.MessageEntityTypeTextLink, URL: "http://x.test", Offset: 6, Length: 4},
		},
	}
	got := ToDomainMessage(m)
	if len(got.Entities) != 1 || got.Entities[0].URL != "http://x.test" || got.Entities[0].Type != "text_link" {
		t.Fatalf("entities not extracted: %+v", got.Entities)
	}
}

func TestToDomainMessageExtractsDetectionSurfaces(t *testing.T) {
	m := &models.Message{
		ID:        6,
		Chat:      models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From:      &models.User{ID: 7},
		Text:      "caption-less",
		SenderTag: "premium",
		EditDate:  1700100,
		Photo:     []models.PhotoSize{{FileID: "abc"}},
		ExternalReply: &models.ExternalReplyInfo{
			Origin: models.MessageOrigin{Type: models.MessageOriginTypeUser},
		},
		Quote: &models.TextQuote{Text: "quoted text"},
		Poll: &models.Poll{
			Options: []models.PollOption{{Text: "opt A"}, {Text: "opt B"}},
		},
	}
	got := ToDomainMessage(m)
	if got.SenderTag != "premium" {
		t.Fatalf("bad sender tag: %+v", got.SenderTag)
	}
	if got.EditDate != 1700100 {
		t.Fatalf("bad edit date: %+v", got.EditDate)
	}
	if !got.HasMedia {
		t.Fatalf("expected HasMedia true")
	}
	if got.ExternalReplyText != "quoted text" {
		t.Fatalf("bad external reply text: %+v", got.ExternalReplyText)
	}
	if len(got.PollOptionTexts) != 2 || got.PollOptionTexts[0] != "opt A" || got.PollOptionTexts[1] != "opt B" {
		t.Fatalf("bad poll option texts: %+v", got.PollOptionTexts)
	}
}

func TestToDomainMessageCaptionEntities(t *testing.T) {
	m := &models.Message{
		ID:      8,
		Chat:    models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From:    &models.User{ID: 7},
		Caption: "look",
		CaptionEntities: []models.MessageEntity{
			{Type: models.MessageEntityTypeURL, Offset: 0, Length: 4},
		},
		Document: &models.Document{FileID: "doc1"},
	}
	got := ToDomainMessage(m)
	if len(got.Entities) != 1 || got.Entities[0].Type != "url" {
		t.Fatalf("caption entities not extracted: %+v", got.Entities)
	}
	if !got.HasMedia {
		t.Fatalf("expected HasMedia true for document")
	}
}
