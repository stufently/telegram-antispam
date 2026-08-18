// Command tg-antispam is the single-process antispam bot.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/time/rate"

	"github.com/stufently/telegram-antispam/internal/admin"
	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/queue"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/version"
)

// globalRateLimit and globalRateBurst bound total outbound Telegram calls
// across all chats; perChatRateLimit/perChatRateBurst bound calls to any one
// chat, well under Telegram's per-chat and global send limits.
const (
	globalRateLimit = 25
	globalRateBurst = 25
	perChatRateRPS  = 1
	perChatBurst    = 3
)

// priorityFor maps a Port method name to its queue priority: destructive
// moderation calls (delete/ban/restrict) jump ahead of notifications and
// bookkeeping.
func priorityFor(method string) queue.Priority {
	switch method {
	case "DeleteMessages", "BanMember", "RestrictMember", "BanSenderChat":
		return queue.PrioHigh
	default:
		return queue.PrioNormal
	}
}

// chatLimiters lazily builds and caches one rate.Limiter per chat, shared by
// every LivePort call for that chat.
type chatLimiters struct {
	mu  sync.Mutex
	lim map[int64]*rate.Limiter
}

func newChatLimiters() *chatLimiters {
	return &chatLimiters{lim: map[int64]*rate.Limiter{}}
}

func (c *chatLimiters) get(chat int64) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.lim[chat]
	if !ok {
		l = rate.NewLimiter(rate.Limit(perChatRateRPS), perChatBurst)
		c.lim[chat] = l
	}
	return l
}

func main() {
	log.Printf("tg-antispam %s starting", version.String())

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfgStore := config.NewStore(cfg)

	db, err := store.Open(os.Getenv("DB_PATH"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	seq := telegram.NewSequencer()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// shutdownCtx names ctx distinctly for use inside the default handler
	// below, whose own ctx parameter shadows this one: callback jobs run
	// asynchronously via the sequencer, after the handler call that
	// submitted them returns, so they must observe process shutdown
	// (shutdownCtx) rather than the update-scoped ctx the library hands the
	// handler, which the library may treat as done once the handler returns.
	shutdownCtx := ctx

	go func() {
		if err := cfgStore.Watch(ctx, cfgPath); err != nil {
			log.Printf("config watcher stopped: %v", err)
		}
	}()

	// The dispatcher owns every outbound Telegram call: global + per-chat
	// rate limiting, priority ordering, 429 retry.
	limiters := newChatLimiters()
	disp := queue.NewDispatcher(rate.NewLimiter(rate.Limit(globalRateLimit), globalRateBurst), limiters.get)
	dispDone := make(chan struct{})
	go func() {
		disp.Run(ctx)
		close(dispDone)
	}()

	// handler and adminHandler are wired below, once the *bot.Bot they
	// depend on (via LivePort) exists. The default handler closures below
	// capture these pointers by reference — populated before b.Start runs,
	// which is the only point long polling (and therefore any inbound
	// update) can occur.
	var (
		handler      *telegram.Handler
		adminHandler *admin.Handler
	)

	opts := []tgbot.Option{
		// The library otherwise runs every handler in its own untracked
		// goroutine (`go r(ctx, b, upd)` in ProcessUpdate), so b.Start could
		// return — letting shutdown call handler.Stop() — while a handler
		// goroutine is still mid-flight and about to touch the album
		// buffer. WithNotAsyncHandlers runs handlers inline in the polling
		// loop instead, so b.Start's internal WaitGroup only completes once
		// the last handler call has returned.
		tgbot.WithNotAsyncHandlers(),
		tgbot.WithAllowedUpdates([]string{
			"message", "edited_message", "callback_query",
			"chat_member", "my_chat_member", "message_reaction",
		}),
		tgbot.WithDefaultHandler(func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
			switch {
			case update.Message != nil:
				handler.OnMessage(ctx, update.ID, telegram.ToDomainMessage(update.Message))
			case update.EditedMessage != nil:
				handler.OnEditedMessage(ctx, update.ID, telegram.ToDomainMessage(update.EditedMessage))
			case update.CallbackQuery != nil:
				cb := update.CallbackQuery
				// Offload onto the sequencer: WithNotAsyncHandlers runs this
				// inline on the single poll-consumer goroutine, and
				// adminHandler.Handle does DB + GetChatAdministrators
				// (network) + AnswerCallback work that would otherwise stall
				// polling. All admin callbacks share one bucket (cfg's admin
				// chat id) — fine since they're low volume; seq.Wait() at
				// shutdown drains this job like any other. Use shutdownCtx
				// (the process's shutdown-aware context, same one passed to
				// handler.SetContext), not the per-update ctx the library
				// hands this closure, since the job runs after this handler
				// call returns.
				seq.Submit(cfgStore.Current().AdminChatID, func() {
					err := adminHandler.Handle(shutdownCtx, admin.Callback{
						ID:        cb.ID,
						Data:      cb.Data,
						PresserID: cb.From.ID,
					})
					if err != nil {
						log.Printf("admin callback: %v", err)
					}
				})
			}
		}),
	}

	b, err := tgbot.New(cfg.BotToken, opts...)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	livePort := telegram.NewLivePort(b, disp, priorityFor)
	machine := incident.New(livePort, db, cfg.AdminChatID)
	machine.SetButtons(admin.Buttons)
	handler = telegram.NewHandler(db, seq, cfgStore, machine)
	handler.SetContext(ctx) // so an album flush triggered off-request during shutdown observes cancellation instead of blocking forever
	adminHandler = admin.NewHandler(livePort, db, operatorSet(cfg))

	log.Print("long polling started")
	b.Start(ctx) // blocks until ctx is cancelled (SIGINT/SIGTERM)

	// Graceful shutdown, in dependency order: stop producing new work (flush
	// any buffered album parts into the sequencer), drain everything the
	// sequencer still owns (which may submit final jobs to the dispatcher),
	// then let the dispatcher finish/exit, and only then close the db that
	// sequencer jobs depend on. A plain defer chain would run these in
	// declaration-reversed order and could drain the dispatcher before the
	// sequencer's last jobs reach it, or close the db while jobs still use
	// it — hence the explicit order here instead.
	handler.Stop()
	seq.Wait()
	<-dispDone
}

// operatorSet builds the global-operator set admin.NewHandler expects. M2's
// config has no operators field yet, so this is empty (source-chat admins
// can still act); a later milestone can add cfg.Operators and populate it
// here.
func operatorSet(cfg *config.Config) map[int64]bool {
	return map[int64]bool{}
}
