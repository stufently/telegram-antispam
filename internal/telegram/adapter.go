package telegram

import (
	"path"
	"strings"

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

	mediaKinds := collectMediaKinds(m)
	documentExtensions, documentMIMETypes := documentMetadata(m.Document)

	// A forward is recorded as two facts, not one: that it IS a forward, and
	// whether it came from a channel/group rather than a person. Only the
	// second shape is the one spam takes (a relayed casino post), and a
	// detector that cannot tell them apart would have to treat "my friend
	// forwarded me this" identically.
	forwarded := m.ForwardOrigin != nil
	forwardedFromChat := false
	if m.ForwardOrigin != nil {
		forwardedFromChat = m.ForwardOrigin.MessageOriginChannel != nil ||
			m.ForwardOrigin.MessageOriginChat != nil
	}

	// Join/leave service messages are the chat's own noise: they have no
	// author to moderate and no text to read, and a chat that removes
	// spammers all day accumulates a wall of them.
	serviceKind := ""
	switch {
	case len(m.NewChatMembers) > 0:
		serviceKind = "join"
	case m.LeftChatMember != nil:
		serviceKind = "leave"
	}

	// Only bots can attach an inline keyboard, so its presence under an
	// ordinary member's message means the message was produced by a bot.
	hasKeyboard := m.ReplyMarkup != nil && len(m.ReplyMarkup.InlineKeyboard) > 0
	// via_bot is what makes a keyboard ordinary: an inline-bot result (a gif
	// from @gif, a poll from a quiz bot) carries one and is posted by a real
	// member on purpose. Recording it separately keeps "has buttons" a fact
	// rather than an accusation.
	viaBot := m.ViaBot != nil

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
		MediaKinds:         mediaKinds,
		DocumentExtensions: documentExtensions,
		DocumentMIMETypes:  documentMIMETypes,
		Forwarded:          forwarded,
		ForwardedFromChat:  forwardedFromChat,
		HasKeyboard:        hasKeyboard,
		ViaBot:             viaBot,
		ServiceKind:        serviceKind,
		ReplyTo:            replyTo,
	}
}

// documentMetadata keeps only the type information needed for moderation.
// The filename itself is attacker-controlled and may contain personal data;
// retaining it would widen both persisted audit data and the optional LLM
// payload for no detection benefit beyond its final extension.
func documentMetadata(doc *models.Document) (extensions, mimeTypes []string) {
	if doc == nil {
		return nil, nil
	}
	if extension := strings.ToLower(path.Ext(strings.TrimSpace(doc.FileName))); extension != "" {
		extensions = []string{extension}
	}
	if mimeType := strings.ToLower(strings.TrimSpace(doc.MimeType)); mimeType != "" {
		mimeTypes = []string{mimeType}
	}
	return extensions, mimeTypes
}

// collectMediaKinds lists the attachment types present on a message, using
// the Bot API's own field names so a config value or an audit row reads the
// same as the API docs. Order is fixed (not map iteration) so a signal
// detail is stable across restarts and diffable in the audit table.
//
// Poll, dice, game, venue and location are deliberately included: each is a
// message with no text a text detector can read, which is the whole point of
// tracking media at all.
func collectMediaKinds(m *models.Message) []string {
	kinds := make([]string, 0, 2)
	add := func(present bool, name string) {
		if present {
			kinds = append(kinds, name)
		}
	}
	add(m.Photo != nil, "photo")
	add(m.PaidMedia != nil, "paid_media")
	add(m.LivePhoto != nil, "live_photo")
	add(m.Video != nil, "video")
	add(m.VideoNote != nil, "video_note")
	add(m.Animation != nil, "animation")
	add(m.Audio != nil, "audio")
	add(m.Voice != nil, "voice")
	add(m.Document != nil, "document")
	add(m.Sticker != nil, "sticker")
	add(m.Story != nil, "story")
	add(m.Contact != nil, "contact")
	add(m.Poll != nil, "poll")
	add(m.Dice != nil, "dice")
	add(m.Game != nil, "game")
	add(m.Venue != nil, "venue")
	add(m.Location != nil, "location")
	add(m.Invoice != nil, "invoice")
	add(m.Checklist != nil, "checklist")
	if len(kinds) == 0 {
		return nil
	}
	return kinds
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
