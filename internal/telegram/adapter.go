package telegram

import (
	"github.com/go-telegram/bot/models"
	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
)

// ToDomainMessage maps a library message to the domain envelope, classifying
// the sender. This is the boundary where library types stop.
func ToDomainMessage(m *models.Message) domain.Message {
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	in := detect.ClassifyInput{
		ChatID:             m.Chat.ID,
		IsAutomaticForward: m.IsAutomaticForward,
	}
	sender := domain.Sender{}
	if m.From != nil {
		in.FromID = m.From.ID
		in.IsBot = m.From.IsBot
		sender.UserID = m.From.ID
		sender.Username = m.From.Username
		sender.DisplayName = m.From.FirstName
	}
	if m.SenderChat != nil {
		in.SenderChatID = m.SenderChat.ID
		in.SenderChatType = string(m.SenderChat.Type)
		sender.SenderChatID = m.SenderChat.ID
	}
	sender.Kind = detect.ClassifySender(in)

	return domain.Message{
		ChatID:             m.Chat.ID,
		MessageID:          m.ID,
		ThreadID:           m.MessageThreadID,
		MediaGroupID:       m.MediaGroupID,
		Sender:             sender,
		Text:               text,
		Date:               int64(m.Date),
		IsAutomaticForward: m.IsAutomaticForward,
	}
}
