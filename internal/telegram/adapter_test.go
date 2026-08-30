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
		ID: 5, Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
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
	if !got.HasMedia() {
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
		Document: &models.Document{
			FileID:   "doc1",
			FileName: "Поиск Пропавших.APK",
			MimeType: "Application/Vnd.Android.Package-Archive ",
		},
	}
	got := ToDomainMessage(m)
	if len(got.Entities) != 1 || got.Entities[0].Type != "url" {
		t.Fatalf("caption entities not extracted: %+v", got.Entities)
	}
	if !got.HasMedia() {
		t.Fatalf("expected HasMedia true for document")
	}
	if len(got.DocumentExtensions) != 1 || got.DocumentExtensions[0] != ".apk" ||
		len(got.DocumentMIMETypes) != 1 || got.DocumentMIMETypes[0] != "application/vnd.android.package-archive" {
		t.Fatalf("document metadata = extensions %v MIME types %v", got.DocumentExtensions, got.DocumentMIMETypes)
	}
}

// The LLM stage reads the reply parent's attachment type to make sense of a
// bare comment under a carrier message, so the adapter must carry that type
// across — and, as everywhere else, only the type: the filename stays out.
func TestToDomainMessageReplyParentCarriesDocumentMetadata(t *testing.T) {
	m := &models.Message{
		ID:   8,
		Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 7},
		Text: "Обновили наконец !",
		ReplyToMessage: &models.Message{
			ID:   7,
			Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
			From: &models.User{ID: 7},
			Document: &models.Document{
				FileID:   "doc1",
				FileName: "Play VPN.apk",
				MimeType: "application/vnd.android.package-archive",
			},
		},
	}
	got := ToDomainMessage(m)
	if got.ReplyTo == nil {
		t.Fatal("reply parent dropped")
	}
	if len(got.ReplyTo.MediaKinds) != 1 || got.ReplyTo.MediaKinds[0] != "document" {
		t.Fatalf("parent media kinds = %v, want [document]", got.ReplyTo.MediaKinds)
	}
	if len(got.ReplyTo.DocumentExtensions) != 1 || got.ReplyTo.DocumentExtensions[0] != ".apk" {
		t.Fatalf("parent document extensions = %v, want [.apk]", got.ReplyTo.DocumentExtensions)
	}
}

func TestToDomainMessageMediaKindsAreNamedNotJustCounted(t *testing.T) {
	m := &models.Message{
		ID:    9,
		Chat:  models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From:  &models.User{ID: 7},
		Photo: []models.PhotoSize{{FileID: "p"}},
		Video: &models.Video{FileID: "v"},
	}
	got := ToDomainMessage(m)
	if len(got.MediaKinds) != 2 || got.MediaKinds[0] != "photo" || got.MediaKinds[1] != "video" {
		t.Fatalf("media kinds = %v, want [photo video] in a stable order", got.MediaKinds)
	}
}

func TestToDomainMessageForwardFromChannelIsDistinguishedFromAPerson(t *testing.T) {
	fromChannel := &models.Message{
		ID:   10,
		Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 7},
		ForwardOrigin: &models.MessageOrigin{
			Type:                 models.MessageOriginTypeChannel,
			MessageOriginChannel: &models.MessageOriginChannel{},
		},
	}
	got := ToDomainMessage(fromChannel)
	if !got.Forwarded || !got.ForwardedFromChat {
		t.Fatalf("channel forward: forwarded=%v fromChat=%v", got.Forwarded, got.ForwardedFromChat)
	}

	fromUser := &models.Message{
		ID:   11,
		Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 7},
		ForwardOrigin: &models.MessageOrigin{
			Type:              models.MessageOriginTypeUser,
			MessageOriginUser: &models.MessageOriginUser{},
		},
	}
	got = ToDomainMessage(fromUser)
	if !got.Forwarded {
		t.Fatal("a forward from a person is still a forward")
	}
	if got.ForwardedFromChat {
		t.Fatal("a forward from a person must not read as a relayed channel post")
	}
}

func TestToDomainMessageKeyboardAndViaBotTravelTogether(t *testing.T) {
	m := &models.Message{
		ID:          12,
		Chat:        models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From:        &models.User{ID: 7},
		ViaBot:      &models.User{ID: 42, IsBot: true},
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "go"}}}},
	}
	got := ToDomainMessage(m)
	if !got.HasKeyboard {
		t.Fatal("inline keyboard not detected")
	}
	if !got.ViaBot {
		t.Fatal("via_bot is the innocent explanation for the keyboard and must be recorded")
	}
}

func TestToDomainMessageNoMediaLeavesKindsEmpty(t *testing.T) {
	m := &models.Message{
		ID:   13,
		Chat: models.Chat{ID: -100123, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 7},
		Text: "просто текст",
	}
	got := ToDomainMessage(m)
	if got.HasMedia() || got.MediaKinds != nil {
		t.Fatalf("plain text must carry no media kinds, got %v", got.MediaKinds)
	}
}
