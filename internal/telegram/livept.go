// Package telegram: LivePort is the library-backed Port. Every outbound
// call is submitted to a queue.Dispatcher, which owns rate limiting,
// priority ordering, and 429 retry — LivePort's job is only to build the
// library params, unwrap a terminal result, and translate a 429 into a
// queue.RetryAfter so the dispatcher retries the job for us.
package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"

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

	mu     sync.Mutex // guards selfID
	selfID int64      // bot's own user id, resolved once via GetMe
}

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

// Self returns the bot's own user id, resolving it via GetMe on first use.
// Startup calls it as an explicit connectivity/token probe: Bot.New is
// constructed WithSkipGetMe so that the very first identity lookup is routed
// through the dispatcher like every other call, which means this is the only
// thing standing between a revoked token and a process that polls forever
// while reporting itself healthy.
func (p *LivePort) Self(ctx context.Context) (int64, error) {
	return p.me(ctx, 0)
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
			return mapRetry(err)
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

func (p *LivePort) SendAdmin(ctx context.Context, adminChat int64, msg AdminMessage) (int, error) {
	return submitSync(ctx, p.disp, adminChat, p.prio("SendAdmin"), func(ctx context.Context) (int, error) {
		params := &bot.SendMessageParams{ChatID: adminChat, Text: msg.Text}
		if len(msg.Buttons) > 0 {
			params.ReplyMarkup = models.InlineKeyboardMarkup{InlineKeyboard: toInlineKeyboard(msg.Buttons)}
		}
		res, err := p.b.SendMessage(ctx, params)
		if err != nil {
			return 0, mapRetry(err)
		}
		return res.ID, nil
	})
}

func (p *LivePort) BanSenderChat(ctx context.Context, chat, senderChat int64) error {
	return submitSyncErr(ctx, p.disp, chat, p.prio("BanSenderChat"), func(ctx context.Context) error {
		_, err := p.b.BanChatSenderChat(ctx, &bot.BanChatSenderChatParams{ChatID: chat, SenderChatID: senderChat})
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
	return submitSync(ctx, p.disp, chat, p.prio("SendEphemeral"), func(ctx context.Context) (int, error) {
		msg, err := p.b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, ReceiverUserID: userID, Text: text})
		if err != nil {
			return 0, mapRetry(err)
		}
		return msg.EphemeralMessageID, nil
	})
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
