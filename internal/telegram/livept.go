// Package telegram: LivePort is the library-backed Port. Every outbound
// call is submitted to a queue.Dispatcher, which owns rate limiting,
// priority ordering, and 429 retry — LivePort's job is only to build the
// library params, unwrap a terminal result, and translate a 429 into a
// queue.RetryAfter so the dispatcher retries the job for us.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	bot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/stufently/telegram-antispam/internal/queue"
)

// LivePort implements Port against a real *bot.Bot, routing every call
// through disp so outbound Telegram traffic is rate-limited, priority
// ordered, and 429-retried in one place.
type LivePort struct {
	b    *bot.Bot
	disp *queue.Dispatcher
	prio func(method string) queue.Priority

	mu     sync.Mutex // guards selfID and titles
	selfID int64      // bot's own user id, resolved once via GetMe
	titles map[int64]titleEntry
}

// titleEntry is one cached chat title. Titles are cached because the admin
// card needs one per incident and a chat is renamed far more rarely than it
// is moderated; the TTL bounds how long a rename stays invisible.
type titleEntry struct {
	title string
	at    time.Time
}

// chatTitleTTL is how long a fetched chat title is reused, and maxTitleCache
// bounds how many chats are remembered at once.
const (
	chatTitleTTL  = time.Hour
	maxTitleCache = 512
)

var _ Port = (*LivePort)(nil)

// NewLivePort builds a Port that submits every call as a queue.Job on disp.
// prio maps a Port method name (e.g. "DeleteMessages") to the queue.Priority
// its jobs should run at.
func NewLivePort(b *bot.Bot, disp *queue.Dispatcher, prio func(method string) queue.Priority) *LivePort {
	return &LivePort{b: b, disp: disp, prio: prio}
}

// me resolves and caches the bot's own user id. GetMe goes through the same
// dispatcher as every other outbound Port call, so rate limits and 429 retry
// apply consistently. It caches only on success, so a transient terminal
// failure is retried on the next call rather than poisoning later checks.
func (p *LivePort) me(ctx context.Context, chat int64) (int64, error) {
	p.mu.Lock()
	id := p.selfID
	p.mu.Unlock()
	if id != 0 {
		return id, nil
	}
	// Resolve outside the lock so a slow/unreachable GetMe at boot cannot
	// serialize concurrent self-checks behind the mutex. Two racing callers
	// may both call GetMe once; that is harmless and idempotent.
	u, err := submitSync(ctx, p.disp, chat, p.prio("GetMe"), func(ctx context.Context) (*models.User, error) {
		u, err := p.b.GetMe(ctx)
		return u, mapRetry(err)
	})
	if err != nil {
		return 0, mapRetry(err)
	}
	p.mu.Lock()
	p.selfID = u.ID
	p.mu.Unlock()
	return u.ID, nil
}

// SelfUsername returns the bot's @username (without the "@"). Commands are
// addressed as "/spam@thisbot" in chats that host several bots, and refusing
// to answer a command addressed to a different bot requires knowing our own
// name. Unlike Self it is not cached: it is called once at startup.
func (p *LivePort) SelfUsername(ctx context.Context) (string, error) {
	u, err := submitSync(ctx, p.disp, 0, p.prio("GetMe"), func(ctx context.Context) (*models.User, error) {
		u, err := p.b.GetMe(ctx)
		return u, mapRetry(err)
	})
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// Self returns the bot's own user id, resolving it via GetMe on first use.
// Startup calls it as an explicit connectivity/token probe: Bot.New is
// constructed WithSkipGetMe so that the very first identity lookup is routed
// through the dispatcher like every other call, which means this is the only
// thing standing between a revoked token and a process that polls forever
// while reporting itself healthy.
func (p *LivePort) Self(ctx context.Context) (int64, error) {
	return p.me(ctx, 0)
}

// GroupPrivacy reports whether Telegram's Group Privacy mode is ON for this
// bot, i.e. whether it is BLIND to ordinary group messages.
//
// This is the single most consequential setting the bot cannot change from
// code: with privacy on it receives only commands aimed at it, replies to
// its own messages and service messages — so every detector sees nothing and
// the process looks perfectly healthy while moderating an empty stream.
// It is checked at startup because the failure has no other symptom: no
// error, no restart, just silence that reads like a quiet chat.
func (p *LivePort) GroupPrivacy(ctx context.Context) (bool, error) {
	u, err := submitSync(ctx, p.disp, 0, p.prio("GetMe"), func(ctx context.Context) (*models.User, error) {
		u, err := p.b.GetMe(ctx)
		return u, mapRetry(err)
	})
	if err != nil {
		return false, err
	}
	return !u.CanReadAllGroupMessages, nil
}

// Ping performs a GetMe that deliberately BYPASSES the cached identity, so
// it is a real round trip to Telegram and not a map lookup. It is the
// liveness probe: it exercises the same rate limiter, dispatcher and HTTP
// client as every moderation call, so it fails when that path is wedged.
func (p *LivePort) Ping(ctx context.Context) error {
	_, err := submitSync(ctx, p.disp, 0, p.prio("GetMe"), func(ctx context.Context) (*models.User, error) {
		u, err := p.b.GetMe(ctx)
		return u, mapRetry(err)
	})
	return err
}

// CheckBotRights reports the bot's own admin rights in chat plus whether the
// chat has native Aggressive Anti-Spam enabled (spec §13). Owners implicitly
// have every right; a non-admin bot reports IsAdmin=false with no rights.
func (p *LivePort) CheckBotRights(ctx context.Context, chat int64) (BotRights, error) {
	selfID, err := p.me(ctx, chat)
	if err != nil {
		return BotRights{}, err
	}
	return submitSync(ctx, p.disp, chat, p.prio("CheckBotRights"), func(ctx context.Context) (BotRights, error) {
		cm, err := p.b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat, UserID: selfID})
		if err != nil {
			return BotRights{}, mapRetry(err)
		}
		var r BotRights
		switch cm.Type {
		case models.ChatMemberTypeOwner:
			r.IsAdmin, r.CanDelete, r.CanRestrict = true, true, true
		case models.ChatMemberTypeAdministrator:
			if cm.Administrator != nil {
				r.IsAdmin = true
				r.CanDelete = cm.Administrator.CanDeleteMessages
				r.CanRestrict = cm.Administrator.CanRestrictMembers
			}
		}
		full, err := p.b.GetChat(ctx, &bot.GetChatParams{ChatID: chat})
		if err != nil {
			return BotRights{}, mapRetry(err)
		}
		r.AggressiveAntiSpam = full.HasAggressiveAntiSpamEnabled
		return r, nil
	})
}

// batchIDs splits ids into chunks of at most size (Telegram deleteMessages caps at 100).
func batchIDs(ids []int, size int) [][]int {
	var out [][]int
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}

// mapRetry detects the library's 429 error and returns queue.RetryAfter{Seconds: n}
// so the dispatcher backs off and re-runs the job; any other error (including
// nil) passes through unchanged.
//
// The primary path type-asserts the library's *bot.TooManyRequestsError,
// whose RetryAfter field carries Telegram's retry_after seconds directly
// (confirmed against go-telegram/bot v1.23.0's raw_request.go: on HTTP 429
// it returns this concrete type, unwrapped, with Parameters.RetryAfter). The
// string-match branch below is a defensive fallback only, in case a future
// library version wraps or renames that error.
// ignoreAlreadyGone turns "the message is not there any more" into success.
//
// Deleting a message that no longer exists is the goal state, not a failure:
// the author may have deleted it, another admin may have, or — for as long
// as tg-spam runs beside this bot on the same chats — the other bot got
// there first. Treating that as an error aborted the incident before it
// reached its final state, so a perfectly moderated message was recorded as
// half-processed and logged as broken.
func ignoreAlreadyGone(err error) error {
	if err == nil {
		return nil
	}
	// Deliberately ONLY "not found". "message can't be deleted" is a
	// different animal — revoked rights, or a message too old to delete —
	// and swallowing it would hide the one failure an operator must act on.
	if strings.Contains(strings.ToLower(err.Error()), "message to delete not found") {
		return nil
	}
	return err
}

func mapRetry(err error) error {
	if err == nil {
		return nil
	}
	var tmr *bot.TooManyRequestsError
	if errors.As(err, &tmr) {
		seconds := tmr.RetryAfter
		if seconds <= 0 {
			seconds = 1
		}
		return queue.RetryAfter{Seconds: seconds}
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "too many requests") || strings.Contains(msg, "retry_after") || strings.Contains(msg, "retry after") {
		return queue.RetryAfter{Seconds: 1}
	}
	return err
}

// submitSync submits a job for chat at prio and blocks until it produces a
// terminal (non-retry) result. do is invoked once per attempt; if it returns
// a queue.RetryAfter error, the dispatcher sleeps and re-runs do without
// this call observing anything — only the eventual terminal outcome is
// delivered to the caller via the buffered result channel.
func submitSync[T any](ctx context.Context, disp *queue.Dispatcher, chat int64, prio queue.Priority, do func(ctx context.Context) (T, error)) (T, error) {
	type res struct {
		val T
		err error
	}
	ch := make(chan res, 1)
	disp.Submit(chat, queue.Job{Priority: prio, Run: func(dispatchCtx context.Context) error {
		// A queued request has two owners: the dispatcher lifecycle and the
		// caller waiting for its result. Cancel the actual HTTP attempt when
		// either ends; otherwise a caller can return while stale work continues
		// against Telegram during shutdown.
		attemptCtx, cancelAttempt := context.WithCancel(dispatchCtx)
		stopCallerCancel := context.AfterFunc(ctx, cancelAttempt)
		val, err := do(attemptCtx)
		stopCallerCancel()
		cancelAttempt()
		if ra, ok := err.(queue.RetryAfter); ok {
			return ra // let the dispatcher retry; nothing terminal yet
		}
		ch <- res{val, err}
		return nil
	}})
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-ch:
		return r.val, r.err
	}
}

// submitSyncErr is submitSync for methods that only return an error.
func submitSyncErr(ctx context.Context, disp *queue.Dispatcher, chat int64, prio queue.Priority, do func(ctx context.Context) error) error {
	_, err := submitSync(ctx, disp, chat, prio, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, do(ctx)
	})
	return err
}

func (p *LivePort) CopyMessages(ctx context.Context, dstChat, srcChat int64, ids []int) ([]int, error) {
	return submitSync(ctx, p.disp, dstChat, p.prio("CopyMessages"), func(ctx context.Context) ([]int, error) {
		res, err := p.b.CopyMessages(ctx, &bot.CopyMessagesParams{
			ChatID:     dstChat,
			FromChatID: srcChat,
			MessageIDs: ids,
		})
		if err != nil {
			return nil, mapRetry(err)
		}
		out := make([]int, len(res))
		for i, m := range res {
			out[i] = m.ID
		}
		return out, nil
	})
}

// DeleteMessages splits ids into batches of at most 100 (Telegram's cap) and
// submits one job per batch, so each batch is rate-limited and retried
// independently instead of one 429 replaying already-deleted batches.
func (p *LivePort) DeleteMessages(ctx context.Context, chat int64, ids []int) error {
	prio := p.prio("DeleteMessages")
	for _, batch := range batchIDs(ids, 100) {
		batch := batch
		if err := submitSyncErr(ctx, p.disp, chat, prio, func(ctx context.Context) error {
			_, err := p.b.DeleteMessages(ctx, &bot.DeleteMessagesParams{ChatID: chat, MessageIDs: batch})
			return ignoreAlreadyGone(mapRetry(err))
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *LivePort) BanMember(ctx context.Context, chat, user int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("BanMember"), func(ctx context.Context) error {
		_, err := p.b.BanChatMember(ctx, &bot.BanChatMemberParams{ChatID: chat, UserID: user})
		return mapRetry(err)
	})
}

// UnbanMember lifts a ban with OnlyIfBanned set, which is what makes it a
// safe undo: without that flag unbanChatMember also *removes* a member who is
// currently in the chat (Telegram implements "unban" as kick-then-allow), so
// a mistaken press of the admin-chat undo button would eject the very user it
// is meant to rescue.
func (p *LivePort) UnbanMember(ctx context.Context, chat, user int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("UnbanMember"), func(ctx context.Context) error {
		_, err := p.b.UnbanChatMember(ctx, &bot.UnbanChatMemberParams{
			ChatID:       chat,
			UserID:       user,
			OnlyIfBanned: true,
		})
		return mapRetry(err)
	})
}

// UnrestrictMember lifts a mute by granting EVERY chat permission, which is
// what Telegram requires to actually clear a restriction: restrictChatMember
// replaces the user's permission set wholesale, so a call that only sets the
// can_send_* flags leaves the user unable to invite, pin, react, or change
// info — a half-lifted sanction that looks lifted in the chat member list.
// That is why this is its own method rather than RestrictMember with a
// "true" Perms: Perms exists to describe a mute, and a mute never needs
// those fields.
func (p *LivePort) UnrestrictMember(ctx context.Context, chat, user int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("UnrestrictMember"), func(ctx context.Context) error {
		_, err := p.b.RestrictChatMember(ctx, &bot.RestrictChatMemberParams{
			ChatID: chat,
			UserID: user,
			Permissions: &models.ChatPermissions{
				CanSendMessages:       true,
				CanSendAudios:         true,
				CanSendDocuments:      true,
				CanSendPhotos:         true,
				CanSendVideos:         true,
				CanSendVideoNotes:     true,
				CanSendVoiceNotes:     true,
				CanSendPolls:          true,
				CanSendOtherMessages:  true,
				CanAddWebPagePreviews: true,
				CanChangeInfo:         true,
				CanInviteUsers:        true,
				CanPinMessages:        true,
				CanManageTopics:       true,
				CanReactToMessages:    true,
			},
		})
		return mapRetry(err)
	})
}

// RestrictMember maps the single Perms.CanSend toggle onto every can_send_*
// permission uniformly (full mute / full unmute); until==0 omits UntilDate
// (permanent, by the field's own omitempty), otherwise the caller-supplied
// absolute unix time is passed through as-is.
func (p *LivePort) RestrictMember(ctx context.Context, chat, user int64, perms Perms, until int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("RestrictMember"), func(ctx context.Context) error {
		params := &bot.RestrictChatMemberParams{
			ChatID: chat,
			UserID: user,
			Permissions: &models.ChatPermissions{
				CanSendMessages:       perms.CanSend,
				CanSendAudios:         perms.CanSend,
				CanSendDocuments:      perms.CanSend,
				CanSendPhotos:         perms.CanSend,
				CanSendVideos:         perms.CanSend,
				CanSendVideoNotes:     perms.CanSend,
				CanSendVoiceNotes:     perms.CanSend,
				CanSendPolls:          perms.CanSend,
				CanSendOtherMessages:  perms.CanSend,
				CanAddWebPagePreviews: perms.CanSend,
			},
		}
		if until != 0 {
			params.UntilDate = int(until)
		}
		_, err := p.b.RestrictChatMember(ctx, params)
		return mapRetry(err)
	})
}

// SendAdmin posts the verdict card, threaded onto the evidence copy that was
// sent just before it.
//
// The thread is not cosmetic. Incidents from different source chats are
// processed in parallel, so the admin chat can legitimately show "evidence A,
// evidence B, card B, card A" — and an album contributes several evidence
// messages to a single card. Whoever reviews a false positive later pairs the
// evidence with the verdict, and with nothing but message order to go on that
// pairing can silently be wrong: unbanning a spammer, or refusing to unban a
// person. A reply makes the pairing explicit and order-independent.
//
// msg.CopyMessageIDs is empty exactly when the copy failed (the machine's
// StateEvidenceFailed branch); then the card goes out unthreaded, as before.
func (p *LivePort) SendAdmin(ctx context.Context, adminChat int64, msg AdminMessage) (int, error) {
	return submitSync(ctx, p.disp, adminChat, p.prio("SendAdmin"), func(ctx context.Context) (int, error) {
		params := &bot.SendMessageParams{ChatID: adminChat, Text: msg.Text}
		if len(msg.Buttons) > 0 {
			params.ReplyMarkup = models.InlineKeyboardMarkup{InlineKeyboard: toInlineKeyboard(msg.Buttons)}
		}
		if len(msg.CopyMessageIDs) > 0 && msg.CopyMessageIDs[0] > 0 {
			// First copy of an album is enough to anchor the whole group.
			// The id must be positive: MessageID is omitempty, so a zero would
			// serialize as a reply_parameters naming no target at all, which
			// the Bot API silently treats as "no reply" — a request that reads
			// like a thread and is not one.
			//
			// ChatID is deliberately omitted: the copy lives in adminChat,
			// which is where this send goes, and Bot API reserves that field
			// for a reply whose target is in a DIFFERENT chat.
			//
			// AllowSendingWithoutReply is the first half of "the card must go
			// out no matter what": if a moderator deleted the evidence in the
			// seconds between the copy and this send, Telegram delivers the
			// card unthreaded instead of refusing it.
			params.ReplyParameters = &models.ReplyParameters{
				MessageID:                msg.CopyMessageIDs[0],
				AllowSendingWithoutReply: true,
			}
		}
		res, err := p.b.SendMessage(ctx, params)
		if err != nil && params.ReplyParameters != nil && replyTargetGone(err) {
			// Second half: allow_sending_without_reply is documented not to
			// cover every case (it does not apply across chats or forum
			// topics), so if Telegram still refuses over the reply target,
			// drop the thread and resend. A card without a thread is a
			// degraded card; a missing card is a lost incident.
			params.ReplyParameters = nil
			res, err = p.b.SendMessage(ctx, params)
		}
		if err != nil {
			return 0, mapRetry(err)
		}
		return res.ID, nil
	})
}

// replyTargetGone reports whether err is Telegram refusing a send because the
// message being replied to is no longer there.
//
// The match is deliberately loose ("repl…" plus "not found", or the raw
// MESSAGE_ID_INVALID): Bot API has worded this several ways over the years
// ("message to be replied not found", "reply message not found"), and the
// cost of missing a wording is a dropped verdict card, while the cost of an
// over-match is one extra send of a card that was going out regardless. It
// is only ever consulted for a sendMessage whose sole message id IS the reply
// target, so no other id can be the one Telegram calls invalid.
//
// A 429 is excluded STRUCTURALLY rather than by wording, because that is the
// one error where resending is actively wrong: the dispatcher owns the retry,
// and mapRetry only sees the error if this returns false. Relying on the text
// alone would hand a rate-limit whose description happened to mention the
// reply target straight into an immediate resend.
func replyTargetGone(err error) bool {
	if err == nil {
		return false
	}
	var tooMany *bot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		return false
	}
	m := strings.ToLower(err.Error())
	if strings.Contains(m, "message_id_invalid") {
		return true
	}
	return strings.Contains(m, "repl") && strings.Contains(m, "not found")
}

// ChatTitle returns the chat's human-readable name, cached for chatTitleTTL.
// The title is what makes an admin card readable: the evidence copy carries no
// origin, so the id alone leaves the moderator matching numbers by hand.
func (p *LivePort) ChatTitle(ctx context.Context, chat int64) (string, error) {
	p.mu.Lock()
	if e, ok := p.titles[chat]; ok && time.Since(e.at) < chatTitleTTL {
		p.mu.Unlock()
		return e.title, nil
	}
	p.mu.Unlock()

	title, err := submitSync(ctx, p.disp, chat, p.prio("ChatTitle"), func(ctx context.Context) (string, error) {
		full, err := p.b.GetChat(ctx, &bot.GetChatParams{ChatID: chat})
		if err != nil {
			return "", mapRetry(err)
		}
		if full.Title != "" {
			return full.Title, nil
		}
		// A private chat has no title; the name it does have is a person's.
		return full.Username, nil
	})
	if err != nil {
		return "", err
	}

	p.mu.Lock()
	if p.titles == nil {
		p.titles = map[int64]titleEntry{}
	}
	if len(p.titles) >= maxTitleCache {
		// A long-lived bot in many chats would otherwise grow this map
		// forever. Dropping it whole costs one getChat per active chat and
		// needs no eviction bookkeeping for what is a convenience cache.
		p.titles = map[int64]titleEntry{}
	}
	p.titles[chat] = titleEntry{title: title, at: time.Now()}
	p.mu.Unlock()
	return title, nil
}

func (p *LivePort) BanSenderChat(ctx context.Context, chat, senderChat int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("BanSenderChat"), func(ctx context.Context) error {
		_, err := p.b.BanChatSenderChat(ctx, &bot.BanChatSenderChatParams{ChatID: chat, SenderChatID: senderChat})
		return mapRetry(err)
	})
}

func (p *LivePort) UnbanSenderChat(ctx context.Context, chat, senderChat int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("UnbanSenderChat"), func(ctx context.Context) error {
		_, err := p.b.UnbanChatSenderChat(ctx, &bot.UnbanChatSenderChatParams{ChatID: chat, SenderChatID: senderChat})
		return mapRetry(err)
	})
}

func (p *LivePort) GetChatAdministrators(ctx context.Context, chat int64) ([]Member, error) {
	return submitSync(ctx, p.disp, chat, p.prio("GetChatAdministrators"), func(ctx context.Context) ([]Member, error) {
		res, err := p.b.GetChatAdministrators(ctx, &bot.GetChatAdministratorsParams{ChatID: chat})
		if err != nil {
			return nil, mapRetry(err)
		}
		out := make([]Member, len(res))
		for i, cm := range res {
			out[i] = memberFromChatMember(cm)
		}
		return out, nil
	})
}

// memberFromChatMember flattens the library's tagged-union ChatMember into
// the narrow Member shape Port callers use.
func memberFromChatMember(cm models.ChatMember) Member {
	switch cm.Type {
	case models.ChatMemberTypeOwner:
		if cm.Owner != nil && cm.Owner.User != nil {
			m := memberFromUser(*cm.Owner.User, string(cm.Owner.Status))
			m.CustomTitle = cm.Owner.CustomTitle
			return m
		}
	case models.ChatMemberTypeAdministrator:
		if cm.Administrator != nil {
			m := memberFromUser(cm.Administrator.User, string(cm.Administrator.Status))
			m.CustomTitle = cm.Administrator.CustomTitle
			return m
		}
	case models.ChatMemberTypeMember:
		if cm.Member != nil && cm.Member.User != nil {
			return memberFromUser(*cm.Member.User, string(cm.Member.Status))
		}
	case models.ChatMemberTypeRestricted:
		if cm.Restricted != nil && cm.Restricted.User != nil {
			return memberFromUser(*cm.Restricted.User, string(cm.Restricted.Status))
		}
	case models.ChatMemberTypeLeft:
		if cm.Left != nil && cm.Left.User != nil {
			return memberFromUser(*cm.Left.User, string(cm.Left.Status))
		}
	case models.ChatMemberTypeBanned:
		if cm.Banned != nil && cm.Banned.User != nil {
			return memberFromUser(*cm.Banned.User, string(cm.Banned.Status))
		}
	}
	return Member{Status: string(cm.Type)}
}

// MemberFromChatMember flattens a library ChatMember (e.g. a chat_member
// update's NewChatMember) into the narrow Member type. Exported so the
// command wiring can build a watch.MemberEvent without importing library
// internals or duplicating the tagged-union switch.
func MemberFromChatMember(cm models.ChatMember) Member {
	return memberFromChatMember(cm)
}

func memberFromUser(u models.User, status string) Member {
	return Member{UserID: u.ID, Status: status, Username: u.Username, DisplayName: u.FirstName}
}

func (p *LivePort) AnswerCallback(ctx context.Context, callbackID, text string) error {
	// AnswerCallback has no associated chat; route it through the chat-0
	// bucket shared by chat-less calls.
	return submitSyncErr(ctx, p.disp, 0, p.prio("AnswerCallback"), func(ctx context.Context) error {
		_, err := p.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callbackID, Text: text})
		return mapRetry(err)
	})
}

func (p *LivePort) EditAdminMarkup(ctx context.Context, adminChat int64, messageID int, buttons [][]Button) error {
	return submitSyncErr(ctx, p.disp, adminChat, p.prio("EditAdminMarkup"), func(ctx context.Context) error {
		_, err := p.b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:      adminChat,
			MessageID:   messageID,
			ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: toInlineKeyboard(buttons)},
		})
		return mapRetry(err)
	})
}

func (p *LivePort) DeleteMessageReaction(ctx context.Context, chat int64, messageID int, userID int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("DeleteMessageReaction"), func(ctx context.Context) error {
		_, err := p.b.DeleteMessageReaction(ctx, &bot.DeleteMessageReactionParams{ChatID: chat, MessageID: messageID, UserID: userID})
		return mapRetry(err)
	})
}

func (p *LivePort) SendEphemeral(ctx context.Context, chat, userID int64, text string) (int, error) {
	return p.sendToUser(ctx, "SendEphemeral", chat, userID, text, nil)
}

func (p *LivePort) SendWelcome(ctx context.Context, chat, userID int64, text string) (int, error) {
	return p.sendToUser(ctx, "SendWelcome", chat, userID, text, nil)
}

func (p *LivePort) SendCaptchaEphemeral(ctx context.Context, chat, userID int64, text string, buttons [][]Button) (int, error) {
	return p.sendToUser(ctx, "SendCaptchaEphemeral", chat, userID, text, buttons)
}

func (p *LivePort) ApproveJoinRequest(ctx context.Context, chat, user int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("ApproveJoinRequest"), func(ctx context.Context) error {
		_, err := p.b.ApproveChatJoinRequest(ctx, &bot.ApproveChatJoinRequestParams{ChatID: chat, UserID: user})
		return mapRetry(err)
	})
}

func (p *LivePort) DeclineJoinRequest(ctx context.Context, chat, user int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("DeclineJoinRequest"), func(ctx context.Context) error {
		_, err := p.b.DeclineChatJoinRequest(ctx, &bot.DeclineChatJoinRequestParams{ChatID: chat, UserID: user})
		return mapRetry(err)
	})
}

func (p *LivePort) SendCaptchaMessage(ctx context.Context, chat int64, text string, buttons [][]Button) (int, error) {
	msg, err := submitSync(ctx, p.disp, chat, p.prio("SendCaptchaMessage"), func(ctx context.Context) (*models.Message, error) {
		sent, err := p.b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      chat,
			Text:        text,
			ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: toInlineKeyboard(buttons)},
		})
		if err != nil {
			return nil, mapRetry(err)
		}
		return sent, nil
	})
	if err != nil {
		return 0, err
	}
	if msg == nil {
		return 0, nil
	}
	return msg.ID, nil
}

func (p *LivePort) DeleteEphemeral(ctx context.Context, chat, userID int64, ephemeralID int) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("DeleteEphemeral"), func(ctx context.Context) error {
		_, err := p.b.DeleteEphemeralMessage(ctx, &bot.DeleteEphemeralMessageParams{
			ChatID:             chat,
			ReceiverUserID:     userID,
			EphemeralMessageID: ephemeralID,
		})
		return mapRetry(err)
	})
}

// ephemeralSend is one send-to-user result: ephemeralID is the private id,
// messageID is set when Telegram stored an ordinary chat message instead.
type ephemeralSend struct {
	ephemeralID int
	messageID   int
}

// sendToUser is SendEphemeral and SendWelcome. method is the priority name.
// A message_id without ephemeral_message_id means the text was published to
// the chat. Delete it through the dispatcher — calling DeleteMessages from
// inside this job would deadlock the single-threaded Run — and return
// ErrEphemeralNotHonored, wrapping a delete failure.
func (p *LivePort) sendToUser(ctx context.Context, method string, chat, userID int64, text string, buttons [][]Button) (int, error) {
	sent, err := submitSync(ctx, p.disp, chat, p.prio(method), func(ctx context.Context) (ephemeralSend, error) {
		params := &bot.SendMessageParams{
			ChatID: chat,
			Text:   text,
			EphemeralMessageParameters: &models.EphemeralMessageParameters{
				ReceiverUserID: userID,
			},
		}
		if len(buttons) > 0 {
			params.ReplyMarkup = models.InlineKeyboardMarkup{InlineKeyboard: toInlineKeyboard(buttons)}
		}
		msg, err := p.b.SendMessage(ctx, params)
		if err != nil {
			return ephemeralSend{}, mapRetry(err)
		}
		if msg == nil {
			return ephemeralSend{}, nil
		}
		return ephemeralSend{ephemeralID: msg.EphemeralMessageID, messageID: msg.ID}, nil
	})
	if err != nil {
		return 0, err
	}
	if sent.ephemeralID == 0 && sent.messageID != 0 {
		if delErr := p.DeleteMessages(ctx, chat, []int{sent.messageID}); delErr != nil {
			return 0, fmt.Errorf("%w: %w", ErrEphemeralNotHonored, delErr)
		}
		return 0, ErrEphemeralNotHonored
	}
	return sent.ephemeralID, nil
}

func toInlineKeyboard(buttons [][]Button) [][]models.InlineKeyboardButton {
	out := make([][]models.InlineKeyboardButton, len(buttons))
	for i, row := range buttons {
		r := make([]models.InlineKeyboardButton, len(row))
		for j, b := range row {
			r[j] = models.InlineKeyboardButton{Text: b.Text, CallbackData: b.Data}
		}
		out[i] = r
	}
	return out
}
