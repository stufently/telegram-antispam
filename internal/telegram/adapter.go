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

	entities := toDomainEntities(m.Entities)
	entities = append(entities, toDomainEntities(m.CaptionEntities)...)

	var externalReplyText string
	if m.ExternalReply != nil && m.Quote != nil {
		externalReplyText = m.Quote.Text
	}

	var pollOptionTexts []string
	if m.Poll != nil {
		for _, opt := range m.Poll.Options {
			pollOptionTexts = append(pollOptionTexts, opt.Text)
		}
	}

	hasMedia := m.Photo != nil || m.Video != nil || m.Document != nil ||
		m.Audio != nil || m.Voice != nil || m.Sticker != nil || m.Animation != nil

	// One level only: ToDomainMessage on the reply would recurse through
	// reply_to_message chains, and nothing needs the grandparent.
	var replyTo *domain.Message
	if m.ReplyToMessage != nil {
		r := ToDomainMessage(m.ReplyToMessage)
		r.ReplyTo = nil
		replyTo = &r
	}

	return domain.Message{
		ChatID:             m.Chat.ID,
		MessageID:          m.ID,
		ThreadID:           m.MessageThreadID,
		MediaGroupID:       m.MediaGroupID,
		Sender:             sender,
		Text:               text,
		Date:               int64(m.Date),
		IsAutomaticForward: m.IsAutomaticForward,
		Entities:           entities,
		SenderTag:          m.SenderTag,
		ExternalReplyText:  externalReplyText,
		PollOptionTexts:    pollOptionTexts,
		EditDate:           int64(m.EditDate),
		HasMedia:           hasMedia,
		ReplyTo:            replyTo,
	}
}

// toDomainEntities maps library message entities to domain entities. Type is
// carried as its raw snake_case string (models.MessageEntityType values are
// already the Bot API's snake_case names, e.g. "text_link", "url",
// "mention", "custom_emoji").
func toDomainEntities(src []models.MessageEntity) []domain.Entity {
	if len(src) == 0 {
		return nil
	}
	out := make([]domain.Entity, 0, len(src))
	for _, e := range src {
		out = append(out, domain.Entity{
			Type:   string(e.Type),
			URL:    e.URL,
			Offset: e.Offset,
			Length: e.Length,
		})
	}
	return out
}
