package admin

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
)

// Command is a moderator command typed in a moderated chat (not the admin
// chat), always as a reply to the message it acts on.
type Command string

const (
	// CmdSpam reports the replied-to message as spam: same sanction the
	// cascade would have applied, plus training so the miss is not repeated.
	CmdSpam Command = "spam"
	// CmdHam undoes a sanction on the replied-to message's author and
	// relabels that message as ham.
	CmdHam Command = "ham"
)

// ParseCommand recognizes "/spam" and "/ham" (optionally addressed as
// "/spam@thisbot") as the FIRST token of a message, and nothing else.
//
// Position matters: a chat discussing "/spam" mid-sentence must not trigger
// moderation, and a command addressed to a different bot in the same chat is
// not ours to answer. botUsername is matched case-insensitively and may be
// empty, in which case any @suffix is rejected — better to ignore a command
// than to act on one meant for someone else.
func ParseCommand(text, botUsername string) (Command, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", false
	}
	tok := fields[0]
	if !strings.HasPrefix(tok, "/") {
		return "", false
	}
	name := tok[1:]
	if at := strings.IndexByte(name, '@'); at >= 0 {
		addressee := name[at+1:]
		name = name[:at]
		if botUsername == "" || !strings.EqualFold(addressee, botUsername) {
			return "", false
		}
	}
	switch Command(strings.ToLower(name)) {
	case CmdSpam:
		return CmdSpam, true
	case CmdHam:
		return CmdHam, true
	default:
		return "", false
	}
}

// Commands executes moderator commands. It deliberately owns no moderation
// logic of its own: /spam builds the same domain.Incident the cascade would
// have built and hands it to the incident machine, so evidence copying,
// fail-closed-on-copy-failure, sanction order and the admin-chat undo
// buttons all behave identically whether a human or the detector found the
// spam. A second, parallel implementation of "delete and mute" is exactly
// how the two paths would drift apart.
type Commands struct {
	h           *Handler
	botUsername string
	botID       int64
	report      func(ctx context.Context, inc domain.Incident) (fresh bool, err error)
	relabel     func(chatID int64, from, to string, tokens []string) error
	count       func(cmd Command, result string)
}

// NewCommands builds a Commands bound to an existing Handler, reusing its
// port, store and RBAC. botID identifies this bot so a reply to its own
// messages can be refused.
func NewCommands(h *Handler, botUsername string, botID int64) *Commands {
	return &Commands{h: h, botUsername: strings.TrimPrefix(botUsername, "@"), botID: botID}
}

// SetReporter installs the incident sink — incident.Machine.HandleReport in
// production. Without it /spam refuses to act rather than half-acting.
func (c *Commands) SetReporter(fn func(ctx context.Context, inc domain.Incident) (bool, error)) {
	c.report = fn
}

// SetRelabeler installs the corpus correction hook (train.RelabelTokens
// bound to the deployment's scope policy). A nil relabeler disables
// learning; the sanction still runs.
func (c *Commands) SetRelabeler(fn func(chatID int64, from, to string, tokens []string) error) {
	c.relabel = fn
}

// SetCounter installs an optional metrics hook, called once per handled
// command with the outcome ("ok", "denied", "no_reply", "immune_target",
// "duplicate", "error").
func (c *Commands) SetCounter(fn func(cmd Command, result string)) {
	c.count = fn
}

func (c *Commands) counted(cmd Command, result string) {
	if c.count != nil {
		c.count(cmd, result)
	}
}

// Match reports whether m is a moderator command for this bot. The telegram
// layer calls it before its immunity filter, because anonymous admins post
// as the chat itself and would otherwise be dropped before ever getting here.
func (c *Commands) Match(m domain.Message) bool {
	_, ok := ParseCommand(m.Text, c.botUsername)
	return ok
}

// Handle executes one command. It returns without side effects for anything
// it does not recognize or is not allowed to do; every refusal is reported
// back to the moderator privately, so a command never fails silently.
func (c *Commands) Handle(ctx context.Context, m domain.Message) {
	cmd, ok := ParseCommand(m.Text, c.botUsername)
	if !ok {
		return
	}

	allowed, err := c.authorize(ctx, m)
	if err != nil {
		// Fail closed: an unresolvable admin list means we cannot tell a
		// moderator from a spammer, and both /spam and /ham are destructive.
		log.Printf("command %s chat=%d: authorization unavailable: %v", cmd, m.ChatID, err)
		c.counted(cmd, "auth_error")
		return
	}
	if !allowed {
		c.counted(cmd, "denied")
		c.notify(ctx, m, "Команда доступна только администраторам этого чата.")
		return
	}

	target := m.ReplyTo
	if target == nil {
		c.counted(cmd, "no_reply")
		c.notify(ctx, m, "Ответь этой командой на сообщение, которое нужно обработать.")
		return
	}
	if reason, ok := c.targetRefusal(ctx, m.ChatID, target); !ok {
		c.counted(cmd, "immune_target")
		c.notify(ctx, m, reason)
		return
	}

	switch cmd {
	case CmdSpam:
		c.handleSpam(ctx, m, *target)
	case CmdHam:
		c.handleHam(ctx, m, *target)
	}
}

// authorize resolves whether the command's sender may moderate this chat.
// An anonymous admin posts as the chat itself (sender_chat == chat), which
// Telegram only ever does for that chat's own administrators, so it is
// accepted as such — the alternative is that anonymous admins, often the
// owners, cannot use the commands at all.
func (c *Commands) authorize(ctx context.Context, m domain.Message) (bool, error) {
	if m.Sender.SenderChatID != 0 && m.Sender.SenderChatID == m.ChatID {
		return true, nil
	}
	if m.Sender.UserID == 0 {
		return false, nil
	}
	return c.h.Authorized(ctx, m.ChatID, m.Sender.UserID)
}

// targetRefusal rejects targets no command may touch: the bot itself,
// senderless service messages, and this chat's own administrators (the same
// immunity the cascade grants them — a compromised or careless moderator
// must not be able to turn the bot on the moderators).
func (c *Commands) targetRefusal(ctx context.Context, chatID int64, target *domain.Message) (string, bool) {
	if target.Sender.UserID == c.botID && c.botID != 0 {
		return "Это сообщение бота — обрабатывать его нечего.", false
	}
	if target.Sender.UserID == 0 && target.Sender.SenderChatID == 0 {
		return "У этого сообщения нет автора (служебное) — команда неприменима.", false
	}
	if target.Sender.UserID != 0 {
		isAdmin, err := c.h.Authorized(ctx, chatID, target.Sender.UserID)
		if err != nil {
			return "Не удалось проверить права автора сообщения, команда отменена.", false
		}
		if isAdmin {
			return "Автор сообщения — администратор чата, санкции к нему бот не применяет.", false
		}
	}
	return "", true
}

func (c *Commands) handleSpam(ctx context.Context, cmdMsg domain.Message, target domain.Message) {
	if c.report == nil {
		c.counted(CmdSpam, "error")
		c.notify(ctx, cmdMsg, "Обработчик инцидентов не подключён — обратись к оператору.")
		return
	}

	tokens := detect.Tokenize(detect.Normalize(target))
	inc := domain.Incident{
		ChatID:     target.ChatID,
		MessageIDs: []int{target.MessageID},
		ThreadID:   target.ThreadID,
		Sender:     target.Sender,
		Verdict: domain.Verdict{
			Action: domain.ActionDeleteMute,
			Scope:  domain.ScopeGlobal,
			Reason: "manual_spam",
			Signals: []domain.Signal{{
				Name:   "manual_spam",
				Detail: fmt.Sprintf("by=%d cmd_msg=%d", cmdMsg.Sender.UserID, cmdMsg.MessageID),
			}},
		},
		// A human said "this is spam". Dry-run exists to keep the DETECTOR
		// from acting while it is being evaluated; it was never meant to
		// disarm the moderators, so a manual report acts in any chat.
		DryRun: false,
		Tokens: tokens,
	}

	fresh, err := c.report(ctx, inc)
	switch {
	case err != nil:
		log.Printf("command spam chat=%d msg=%d: %v", target.ChatID, target.MessageID, err)
		c.counted(CmdSpam, "error")
		c.notify(ctx, cmdMsg, "Не смог обработать сообщение: "+err.Error())
		return
	case !fresh:
		// The detector already handled this exact message. Training still
		// runs below: the moderator's label is new information even when the
		// sanction is not.
		c.counted(CmdSpam, "duplicate")
		c.notify(ctx, cmdMsg, "Это сообщение уже обработано ботом — карточка есть в админ-чате.")
	default:
		c.counted(CmdSpam, "ok")
		c.notify(ctx, cmdMsg, c.ackSpam(target))
	}

	c.train(target.ChatID, "ham", "spam", tokens)
	c.deleteCommand(ctx, cmdMsg)
}

func (c *Commands) handleHam(ctx context.Context, cmdMsg domain.Message, target domain.Message) {
	var failed []string
	if target.Sender.UserID != 0 {
		if err := c.h.port.UnrestrictMember(ctx, target.ChatID, target.Sender.UserID); err != nil {
			failed = append(failed, "снять мут: "+err.Error())
		}
		// only_if_banned semantics live in the port: unbanning someone who
		// was never banned is a no-op, which is what makes /ham safe to
		// repeat.
		if err := c.h.port.UnbanMember(ctx, target.ChatID, target.Sender.UserID); err != nil {
			failed = append(failed, "снять бан: "+err.Error())
		}
	}
	if target.Sender.SenderChatID != 0 {
		if err := c.h.port.UnbanSenderChat(ctx, target.ChatID, target.Sender.SenderChatID); err != nil {
			failed = append(failed, "разблокировать канал: "+err.Error())
		}
	}

	tokens := detect.Tokenize(detect.Normalize(target))
	c.train(target.ChatID, "spam", "ham", tokens)

	if len(failed) > 0 {
		log.Printf("command ham chat=%d user=%d: %s", target.ChatID, target.Sender.UserID, strings.Join(failed, "; "))
		c.counted(CmdHam, "error")
		c.notify(ctx, cmdMsg, "Частично: "+strings.Join(failed, "; "))
	} else {
		c.counted(CmdHam, "ok")
		c.notify(ctx, cmdMsg, "Санкции сняты, сообщение учтено как не-спам.")
	}
	c.deleteCommand(ctx, cmdMsg)
}

// train applies the moderator's label, replacing the opposite one if this
// exact message was already learned the other way. Best-effort: a corpus
// write must never undo a sanction that already happened.
func (c *Commands) train(chatID int64, from, to string, tokens []string) {
	if c.relabel == nil || len(tokens) == 0 {
		return
	}
	if err := c.relabel(chatID, from, to, tokens); err != nil {
		log.Printf("command training chat=%d %s->%s: %v", chatID, from, to, err)
	}
}

// deleteCommand removes the command message itself, last and best-effort.
// Last, because a failure to tidy up must not look like a failure to
// moderate; best-effort, because Telegram refuses deletions older than 48
// hours and that is not an error worth surfacing.
func (c *Commands) deleteCommand(ctx context.Context, cmdMsg domain.Message) {
	if err := c.h.port.DeleteMessages(ctx, cmdMsg.ChatID, []int{cmdMsg.MessageID}); err != nil {
		log.Printf("command cleanup chat=%d msg=%d: %v", cmdMsg.ChatID, cmdMsg.MessageID, err)
	}
}

func (c *Commands) ackSpam(target domain.Message) string {
	if target.MediaGroupID != "" {
		// Telegram delivers an album as separate messages and a reply points
		// at exactly one of them; the rest are not discoverable from this
		// update. Saying so is better than leaving the moderator to notice
		// half an album still sitting in the chat.
		return "Сообщение удалено, автор заглушён. ⚠️ Это часть альбома — остальные части удали вручную."
	}
	return "Сообщение удалено, автор заглушён. Откат — кнопками в админ-чате."
}

// notify answers the moderator privately in the chat where the command was
// typed. Telegram shows it only to them, so a refusal does not become a
// public argument in the middle of the chat.
func (c *Commands) notify(ctx context.Context, cmdMsg domain.Message, text string) {
	if cmdMsg.Sender.UserID == 0 {
		return
	}
	if _, err := c.h.port.SendEphemeral(ctx, cmdMsg.ChatID, cmdMsg.Sender.UserID, text); err != nil {
		log.Printf("command reply chat=%d user=%d: %v", cmdMsg.ChatID, cmdMsg.Sender.UserID, err)
	}
}
