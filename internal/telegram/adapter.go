package telegram

import (
	"path"
	"regexp"
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

	// A reply whose parent lives in another chat (a channel, a group the bot
	// is not in) arrives with reply_to_message EMPTY and the parent described
	// in external_reply instead. Its attachment types are read here so the
	// LLM stage sees the same context it gets for an in-chat reply; the parent
	// itself is NOT reconstructed into ReplyTo, because that field is what
	// /spam and /ham act on and it must never point outside this chat.
	var externalReplyMediaKinds, externalReplyDocumentExtensions, externalReplyDocumentMIMETypes []string
	if m.ExternalReply != nil {
		externalReplyMediaKinds = collectExternalReplyMediaKinds(m.ExternalReply)
		externalReplyDocumentExtensions, externalReplyDocumentMIMETypes = collectExternalReplyFileMetadata(m.ExternalReply)
	}

	var pollOptionTexts []string
	if m.Poll != nil {
		for _, opt := range m.Poll.Options {
			pollOptionTexts = append(pollOptionTexts, opt.Text)
		}
	}

	mediaKinds := collectMediaKinds(m)
	documentExtensions, documentMIMETypes := collectFileMetadata(m)

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

		ExternalReplyMediaKinds:         externalReplyMediaKinds,
		ExternalReplyDocumentExtensions: externalReplyDocumentExtensions,
		ExternalReplyDocumentMIMETypes:  externalReplyDocumentMIMETypes,

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

// collectFileMetadata keeps only the type information needed for moderation,
// from EVERY attachment that carries a filename or a MIME type — not just
// document.
//
// Which field a file arrives in is the sender's choice, not a property of the
// file: `list.apk` sent as a video is `video` with `file_name` and `mime_type`
// exactly like the document version, and the Bot API puts `file_name` and
// `mime_type` on video, animation and audio, and `mime_type` on voice and on
// live_photo. Reading only `document` therefore made every attachment rule —
// the `.apk` block among them — bypassable by changing how the client uploads
// the file, while MediaKinds already listed those very types.
//
// paid_media is the one field whose file does not sit on the field itself: it
// is a LIST of items, and the video variant carries a full `Video` (file_name
// and mime_type included) one level down. A collector that only nil-checks the
// top-level field sees "an attachment is here" and reads nothing from it,
// which is the same bypass wearing a different hat — MediaKinds has listed
// `paid_media` all along.
//
// The filename itself is never kept: it is attacker-controlled and may contain
// personal data, and it would widen both the persisted audit row and the
// optional LLM payload for no detection benefit beyond its final extension.
// What is kept is validated, not merely trimmed — see sanitizedExtension.
//
// Order is fixed (and matches collectMediaKinds) so an audit row and a signal
// detail stay stable across restarts and diffable.
func collectFileMetadata(m *models.Message) (extensions, mimeTypes []string) {
	if m == nil {
		return nil, nil
	}
	var meta fileMetadataList
	meta.addPaidMedia(m.PaidMedia)
	if lp := m.LivePhoto; lp != nil {
		// Live photo has no file_name in the Bot API — only a MIME type.
		meta.add("", lp.MimeType)
	}
	if v := m.Video; v != nil {
		meta.add(v.FileName, v.MimeType)
	}
	if a := m.Animation; a != nil {
		meta.add(a.FileName, a.MimeType)
	}
	if a := m.Audio; a != nil {
		meta.add(a.FileName, a.MimeType)
	}
	if v := m.Voice; v != nil {
		// Voice has no file_name in the Bot API — only a MIME type.
		meta.add("", v.MimeType)
	}
	if d := m.Document; d != nil {
		meta.add(d.FileName, d.MimeType)
	}
	return meta.result()
}

// collectExternalReplyFileMetadata is collectFileMetadata for the parent of a
// cross-chat reply. It reads the same seven attachment fields in the same
// order — `external_reply` carries paid_media and live_photo just as a full
// message does — for the same reason collectExternalReplyMediaKinds exists: an
// asymmetry between the two would mean the same `.apk` is seen when it is
// replied to from inside the chat and missed when it is replied to from
// outside — which is precisely the shape the carrier/comment spam pair takes.
func collectExternalReplyFileMetadata(r *models.ExternalReplyInfo) (extensions, mimeTypes []string) {
	if r == nil {
		return nil, nil
	}
	var meta fileMetadataList
	meta.addPaidMedia(r.PaidMedia)
	if lp := r.LivePhoto; lp != nil {
		meta.add("", lp.MimeType)
	}
	if v := r.Video; v != nil {
		meta.add(v.FileName, v.MimeType)
	}
	if a := r.Animation; a != nil {
		meta.add(a.FileName, a.MimeType)
	}
	if a := r.Audio; a != nil {
		meta.add(a.FileName, a.MimeType)
	}
	if v := r.Voice; v != nil {
		meta.add("", v.MimeType)
	}
	if d := r.Document; d != nil {
		meta.add(d.FileName, d.MimeType)
	}
	return meta.result()
}

// fileMetadataList is the shared accumulator behind both collectors, so the
// "sanitize, then append when non-empty" mechanics exist once and the two
// functions differ only in the fields they read.
type fileMetadataList struct {
	extensions []string
	mimeTypes  []string
}

func (f *fileMetadataList) add(fileName, mimeType string) {
	if extension := sanitizedExtension(fileName); extension != "" {
		f.extensions = append(f.extensions, extension)
	}
	if mt := sanitizedMIMEType(mimeType); mt != "" {
		f.mimeTypes = append(f.mimeTypes, mt)
	}
}

// addPaidMedia reads the attachments nested inside a paid-media block. Only
// the video variant carries file metadata: `PaidMediaPreview` is a size/
// duration stub and `PaidMediaPhoto` is a photo, neither of which has a
// file_name or a mime_type to read. The list is walked in the order Telegram
// sent it, so the resulting metadata is as reproducible as every other row.
func (f *fileMetadataList) addPaidMedia(info *models.PaidMediaInfo) {
	if info == nil {
		return
	}
	for _, item := range info.PaidMedia {
		if item.Video != nil {
			f.add(item.Video.Video.FileName, item.Video.Video.MimeType)
		}
	}
}

func (f fileMetadataList) result() (extensions, mimeTypes []string) {
	return f.extensions, f.mimeTypes
}

// extensionPattern is what an extension has to look like to be kept, and it is
// deliberately strict: a short run of ASCII letters and digits after a dot.
//
// The two limits are a conscious trade, not an oversight, and they are drawn
// on the side of never letting the sender's own words out. ASCII-only means a
// non-Latin extension is dropped; 12 characters means `.sqlite-wal` (a hyphen,
// so it fails the alphabet too) and any longer real suffix is dropped as well.
// What is lost is a type name nobody moderates on; what is bought is that a
// filename can never masquerade as one. MediaKinds still says what the
// attachment IS, so a dropped extension costs no detection.
var extensionPattern = regexp.MustCompile(`^\.[a-z0-9]{1,12}$`)

// mimeTypePattern is type/subtype in the RFC 2045 token alphabet, which is
// what "application/vnd.android.package-archive" and every real Telegram MIME
// type look like. Anything else is not a MIME type, whatever it claims.
//
// The 64-character cap per component is below RFC 6838's 127, for the same
// reason as the extension cap: the longest real Telegram MIME type is far
// shorter, and the only thing the extra room could carry is sender text.
var mimeTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]{0,63}/[a-z0-9][a-z0-9!#$&^_.+-]{0,63}$`)

// mimeTopLevelTypes is the complete IANA registry of top-level types (RFC 2046
// plus the later additions). The registry is closed — new top-level types
// require a standards action — so membership can be checked against a literal
// list rather than a shape.
//
// This is what stops "t.me/joinchat" and friends: they satisfy the token
// alphabet perfectly, so shape alone accepts any two words joined by a slash
// and calls the result a MIME type. The subtype cannot be checked the same way
// — it is an open registry with thousands of entries plus legal `x-`/`vnd.`
// space — so a value like "text/answer_ham" still passes. What the closed
// top-level list buys is that whatever passes is at least SHAPED like a media
// type and cannot be an arbitrary phrase or a link.
var mimeTopLevelTypes = map[string]struct{}{
	"application": {},
	"audio":       {},
	"example":     {},
	"font":        {},
	"image":       {},
	"message":     {},
	"model":       {},
	"multipart":   {},
	"text":        {},
	"video":       {},
}

// sanitizedExtension returns the file's extension, or "" when the trailing
// part of the name is not one.
//
// path.Ext is not a validator: it returns EVERYTHING after the last dot, so a
// file named "отчёт.2 подробности в личку" yields an "extension" of
// ".2 подробности в личку" — the attacker-controlled filename, unchanged and
// merely relabelled. That string is persisted in the audit row and, when the
// LLM stage is on, sent to the provider inside the metadata line, where it
// reads as an authoritative fact about the message rather than as text its
// sender chose. Both are exactly what discarding the filename was supposed to
// prevent, and the second is a prompt-injection channel on top.
//
// So the extension is not merely derived, it is checked, and anything that
// does not look like one is dropped silently: a name with no usable extension
// tells moderation nothing that MediaKinds has not already said.
//
// Shape alone is not enough, because digits are legal in an extension and
// legal in far worse things: "report.66812345678" ends in a run of digits that
// the pattern happily accepts, and what would then travel into the audit row
// and the LLM line is a PHONE NUMBER the sender put in the filename — the
// personal data the filename was discarded to avoid, re-emitted under the
// label "extension". A file type is a NAME, so at least one ASCII letter is
// required: every real extension has one (.apk, .mp4, .7z), and a pure number
// after a dot is a version, a date, a serial or a contact — never a type.
func sanitizedExtension(fileName string) string {
	extension := strings.ToLower(path.Ext(strings.TrimSpace(fileName)))
	if !extensionPattern.MatchString(extension) {
		return ""
	}
	if !strings.ContainsFunc(extension, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
		return ""
	}
	return extension
}

// sanitizedMIMEType returns the attachment's MIME type without its parameters,
// or "" when what arrived is not a MIME type at all.
//
// The parameters are cut here rather than only in the rule that compares them
// (detect.canonicalMIMEType): a charset or boundary parameter is sender-
// controlled free text, so leaving it on the value would carry it into the
// audit row and the LLM payload even though no rule ever looks at it.
//
// The value is then checked twice, and the second check is the one that makes
// it a TYPE rather than a string with a slash in it. The token alphabet is
// permissive by design — it has to admit "application/vnd.android.package-
// archive" — so on its own it accepts "t.me/joinchat" too, and the sender
// picks this field. The top-level half is therefore matched against the closed
// IANA registry; the subtype half cannot be (see mimeTopLevelTypes).
func sanitizedMIMEType(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if base, _, found := strings.Cut(mimeType, ";"); found {
		mimeType = strings.TrimSpace(base)
	}
	if !mimeTypePattern.MatchString(mimeType) {
		return ""
	}
	topLevel, _, _ := strings.Cut(mimeType, "/")
	if _, ok := mimeTopLevelTypes[topLevel]; !ok {
		return ""
	}
	return mimeType
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
	var kinds mediaKindList
	kinds.add(m.Photo != nil, "photo")
	kinds.add(m.PaidMedia != nil, "paid_media")
	kinds.add(m.LivePhoto != nil, "live_photo")
	kinds.add(m.Video != nil, "video")
	kinds.add(m.VideoNote != nil, "video_note")
	kinds.add(m.Animation != nil, "animation")
	kinds.add(m.Audio != nil, "audio")
	kinds.add(m.Voice != nil, "voice")
	kinds.add(m.Document != nil, "document")
	kinds.add(m.Sticker != nil, "sticker")
	kinds.add(m.Story != nil, "story")
	kinds.add(m.Contact != nil, "contact")
	kinds.add(m.Poll != nil, "poll")
	kinds.add(m.Dice != nil, "dice")
	kinds.add(m.Game != nil, "game")
	kinds.add(m.Venue != nil, "venue")
	kinds.add(m.Location != nil, "location")
	kinds.add(m.Invoice != nil, "invoice")
	kinds.add(m.Checklist != nil, "checklist")
	return kinds.result()
}

// collectExternalReplyMediaKinds is collectMediaKinds for the parent of a
// cross-chat reply, which Telegram delivers as external_reply rather than as a
// full message. The names and the order are deliberately identical to
// collectMediaKinds: the two lists end up in the same sentence template, and
// a "photo" here against a "picture" there would make the same attachment read
// as two different things depending on where the parent happened to live.
//
// external_reply also carries giveaway and giveaway_winners, which a full
// message has too and collectMediaKinds does not list; they stay out here for
// the same reason, so the two functions keep describing the same vocabulary.
func collectExternalReplyMediaKinds(r *models.ExternalReplyInfo) []string {
	if r == nil {
		return nil
	}
	var kinds mediaKindList
	kinds.add(r.Photo != nil, "photo")
	kinds.add(r.PaidMedia != nil, "paid_media")
	kinds.add(r.LivePhoto != nil, "live_photo")
	kinds.add(r.Video != nil, "video")
	kinds.add(r.VideoNote != nil, "video_note")
	kinds.add(r.Animation != nil, "animation")
	kinds.add(r.Audio != nil, "audio")
	kinds.add(r.Voice != nil, "voice")
	kinds.add(r.Document != nil, "document")
	kinds.add(r.Sticker != nil, "sticker")
	kinds.add(r.Story != nil, "story")
	kinds.add(r.Contact != nil, "contact")
	kinds.add(r.Poll != nil, "poll")
	kinds.add(r.Dice != nil, "dice")
	kinds.add(r.Game != nil, "game")
	kinds.add(r.Venue != nil, "venue")
	kinds.add(r.Location != nil, "location")
	kinds.add(r.Invoice != nil, "invoice")
	kinds.add(r.Checklist != nil, "checklist")
	return kinds.result()
}

// mediaKindList is the shared accumulator behind both collectors, so the
// "append this name when the field is set" mechanics exist once and the two
// lists differ only in the fields they read.
type mediaKindList []string

func (k *mediaKindList) add(present bool, name string) {
	if present {
		*k = append(*k, name)
	}
}

// result returns nil rather than an empty slice, so "no attachment" is one
// value everywhere instead of two.
func (k mediaKindList) result() []string {
	if len(k) == 0 {
		return nil
	}
	return k
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
