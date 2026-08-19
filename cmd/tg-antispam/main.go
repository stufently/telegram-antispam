// Command tg-antispam is the single-process antispam bot.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/time/rate"

	"github.com/stufently/telegram-antispam/internal/admin"
	"github.com/stufently/telegram-antispam/internal/blocklist"
	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/ops"
	"github.com/stufently/telegram-antispam/internal/queue"
	"github.com/stufently/telegram-antispam/internal/selfcheck"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/train"
	"github.com/stufently/telegram-antispam/internal/version"
	"github.com/stufently/telegram-antispam/internal/watch"
)

// historySweepInterval is how often the in-memory detection history
// (duplicate/short-message sliding windows) is swept to drop stale events
// and bound memory usage.
const historySweepInterval = 5 * time.Minute

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

// bayesAdapter adapts *store.DB to detect.BayesSource, converting the
// store's 4-int BayesTotals return into the detect.BayesCounts shape the
// cascade's Bayes stage expects.
type bayesAdapter struct{ db *store.DB }

func (a bayesAdapter) TokenCounts(scope string, tokens []string) (map[string]int, map[string]int, error) {
	return a.db.TokenCounts(scope, tokens)
}

func (a bayesAdapter) Totals(scope string) (detect.BayesCounts, error) {
	sd, hd, st, ht, err := a.db.BayesTotals(scope)
	if err != nil {
		return detect.BayesCounts{}, err
	}
	return detect.BayesCounts{SpamDocs: sd, HamDocs: hd, SpamTokenTotal: st, HamTokenTotal: ht}, nil
}

func main() {
	// Handle import subcommand if present
	if len(os.Args) > 1 && os.Args[1] == "import" {
		added, skipped, err := runImport(os.Args[2:], func(p string) (*store.DB, error) {
			return store.Open(p)
		})
		if err != nil {
			log.Fatalf("import: %v", err)
		}
		log.Printf("imported: added=%d skipped=%d", added, skipped)
		os.Exit(0)
	}

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
	defer func() { _ = db.Close() }()
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

	// reg is the M7 metrics registry: populated at the wiring seams below
	// (the default-handler switch, the decide hook, the blocklist gauge)
	// and served by the ops HTTP server started further down, once cfg.Ops
	// is known to be fully defaulted.
	reg := ops.NewRegistry()
	if *cfg.Ops.MetricsEnabled {
		go func() {
			if err := ops.NewServer(cfg.Ops.MetricsAddr, reg).Run(ctx); err != nil {
				log.Printf("ops server: %v", err)
			}
		}()
	}

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
		handler         *telegram.Handler
		adminHandler    *admin.Handler
		memberWatcher   *watch.MemberWatcher
		reactionCleaner *watch.ReactionCleaner
		selfCheck       func(chat int64)
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
		// Pin single worker: per-chat FIFO ordering (via sequencer), album
		// deduplication, and message-reaction dedup depend on a single inline
		// update consumer; do not raise without a concurrent-ordering strategy.
		tgbot.WithWorkers(1),
		tgbot.WithAllowedUpdates([]string{
			"message", "edited_message", "callback_query",
			"chat_member", "my_chat_member", "message_reaction",
		}),
		tgbot.WithDefaultHandler(func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
			switch {
			case update.Message != nil:
				reg.IncCounter("updates_total", 1, "kind", "message")
				handler.OnMessage(ctx, update.ID, telegram.ToDomainMessage(update.Message))
			case update.EditedMessage != nil:
				reg.IncCounter("updates_total", 1, "kind", "edited")
				handler.OnEditedMessage(ctx, update.ID, telegram.ToDomainMessage(update.EditedMessage))
			case update.CallbackQuery != nil:
				reg.IncCounter("updates_total", 1, "kind", "callback")
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
					// EvidenceText is intentionally left empty here: M2 does
					// not persist the offending message's text/tokens (the
					// admin notification carries only the verdict reason, and
					// the evidence is copied as separate messages), so there
					// is nothing to hand the best-effort Bayes trainer at
					// callback time. Admin-feedback training therefore no-ops
					// in production until a later milestone persists incident
					// tokens (privacy-safe — Bayes stores only counts). The
					// offline `tg-antispam import` path trains fully today.
					err := adminHandler.Handle(shutdownCtx, admin.Callback{
						ID:        cb.ID,
						Data:      cb.Data,
						PresserID: cb.From.ID,
					})
					if err != nil {
						log.Printf("admin callback: %v", err)
					}
				})
			case update.ChatMember != nil:
				reg.IncCounter("updates_total", 1, "kind", "chat_member")
				cm := update.ChatMember
				mem := telegram.MemberFromChatMember(cm.NewChatMember)
				ev := watch.MemberEvent{ChatID: cm.Chat.ID, UserID: mem.UserID, Username: mem.Username, DisplayName: mem.DisplayName}
				seq.Submit(cm.Chat.ID, func() {
					if memberWatcher != nil {
						if err := memberWatcher.Observe(shutdownCtx, ev); err != nil {
							log.Printf("member watch: %v", err)
						}
					}
				})
			case update.MessageReaction != nil:
				reg.IncCounter("updates_total", 1, "kind", "reaction")
				mr := update.MessageReaction
				if mr.User != nil { // only user-attributed reactions (skip anonymous/actor-chat)
					ev := watch.ReactionEvent{ChatID: mr.Chat.ID, MessageID: mr.MessageID, UserID: mr.User.ID, Added: len(mr.NewReaction) > len(mr.OldReaction)}
					seq.Submit(mr.Chat.ID, func() {
						if reactionCleaner != nil {
							if err := reactionCleaner.Observe(shutdownCtx, ev); err != nil {
								log.Printf("reaction cleanup: %v", err)
							}
						}
					})
				}
			case update.MyChatMember != nil:
				// The bot's own membership/rights in a chat changed: re-run the
				// self-check so a revoked can_delete/can_restrict (or a newly
				// enabled native Aggressive Anti-Spam) is surfaced immediately
				// instead of only at the next restart (spec §13).
				reg.IncCounter("updates_total", 1, "kind", "my_chat_member")
				chat := update.MyChatMember.Chat.ID
				seq.Submit(chat, func() {
					if selfCheck != nil {
						selfCheck(chat)
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
	// selfCheck reads the bot's admin rights in one chat and logs any missing
	// rights or hazards (spec §13). Best-effort: a transient read error is
	// logged and dropped, never fatal.
	selfCheck = func(chat int64) {
		warnings, err := selfcheck.Check(shutdownCtx, livePort, chat)
		if err != nil {
			log.Printf("self-check chat %d: %v", chat, err)
			return
		}
		for _, w := range warnings {
			log.Printf("self-check chat %d: %s", chat, w)
		}
	}
	machine := incident.New(livePort, db, cfg.AdminChatID)
	machine.SetButtons(admin.Buttons)
	machine.EphemeralNotice = *cfg.Detection.EphemeralNoticeEnabled
	machine.EphemeralText = cfg.Detection.EphemeralNoticeText
	handler = telegram.NewHandler(db, seq, cfgStore, machine)
	handler.SetContext(ctx) // so an album flush triggered off-request during shutdown observes cancellation instead of blocking forever
	adminHandler = admin.NewHandler(livePort, db, operatorSet(cfg))
	adminHandler.SetTrainer(func(scope, label, text string) error {
		_, err := train.ImportSample(db, scope, label, "user", text)
		return err
	})

	// adminCache is the M5 fake-admin detector's detect.AdminSource: a
	// TTL-cached wrapper over GetChatAdministrators so both the cascade
	// (message-time check) and the member watcher (join/rename-time check)
	// share one cache per chat instead of hitting Telegram on every event.
	adminCache := telegram.NewAdminCache(livePort, time.Duration(cfg.Detection.AdminCacheTTLSeconds)*time.Second)
	memberWatcher = &watch.MemberWatcher{
		Store:       db,
		Admins:      adminCache,
		AdminChatID: cfg.AdminChatID,
		Port:        livePort,
		MaxDistance: cfg.Detection.FakeAdminMaxDistance,
		Enabled:     *cfg.Detection.FakeAdminEnabled,
	}
	reactionCleaner = &watch.ReactionCleaner{
		Spammers: db,
		Port:     livePort,
		Enabled:  *cfg.Detection.ReactionCleanupEnabled,
	}

	// The M3 detection cascade: db satisfies detect.TrustSource (trust is
	// store-backed and durable), while the sliding-window duplicate/short
	// history is in-memory (hist) and swept periodically below so it
	// doesn't grow unbounded. cfg (loaded once at startup) rather than
	// cfgStore.Current() mirrors how machine/handler are already built from
	// the startup snapshot; the config watcher's hot-reload does not
	// currently extend to rebuilding the cascade.
	hist := detect.NewMemHistory()
	behaviorCfg := detect.BehaviorCfg{
		DupThreshold:        *cfg.Detection.Behavior.DupThreshold,
		DupWindow:           cfg.Detection.Behavior.DupWindow.Duration(),
		ShortLen:            *cfg.Detection.Behavior.ShortLen,
		ShortFloodThreshold: *cfg.Detection.Behavior.ShortFloodThreshold,
		ShortWindow:         cfg.Detection.Behavior.ShortWindow.Duration(),
		FlagEdits:           cfg.Detection.Behavior.FlagEdits,
	}
	// Blocklist mirror (LOLS + CAS). Declared as the interface so a disabled
	// blocklist leaves cascade.Blocklist a true nil interface — assigning a
	// typed-nil *blocklist.Blocklist would make `c.Blocklist != nil` true and
	// risk a nil-receiver call. The syncer runs under shutdownCtx like the
	// history sweeper.
	var blocklistSource detect.BlocklistSource
	if *cfg.Blocklist.Enabled {
		bl := blocklist.NewWithConfig(blocklist.Config{
			LolsFullURL:   cfg.Blocklist.LolsFullURL,
			LolsDeltaURL:  cfg.Blocklist.LolsDeltaURL,
			CasFullURL:    cfg.Blocklist.CasFullURL,
			FullInterval:  cfg.Blocklist.FullRefresh.Duration(),
			DeltaInterval: cfg.Blocklist.DeltaRefresh.Duration(),
			HTTPTimeout:   cfg.Blocklist.HTTPTimeout.Duration(),
		})
		go bl.Run(shutdownCtx)
		blocklistSource = bl

		// blocklist_size gauge: sampled on a ticker rather than pushed from
		// the syncer itself, so the ops package stays decoupled from
		// internal/blocklist (see the M7 brief's "keep instrumentation
		// minimal, don't thread the registry deep" guidance).
		go func() {
			t := time.NewTicker(time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					reg.SetGauge("blocklist_size", float64(bl.Len()))
				}
			}
		}()
	}

	cascade := detect.Cascade{
		Trust: db,
		Hist:  hist,
		Rules: detect.Rules{
			DenyStopwords:          cfg.Detection.Rules.DenyStopwords,
			AllowStopwords:         cfg.Detection.Rules.AllowStopwords,
			BlockLinksForUntrusted: *cfg.Detection.Rules.BlockLinksForUntrusted,
			BannedDomains:          cfg.Detection.Rules.BannedDomains,
		},
		Behavior:        behaviorCfg,
		TrustThreshold:  *cfg.Detection.TrustThreshold,
		DefaultAction:   cfg.Action,
		DefaultScope:    domain.ScopeGlobal,
		Bayes:           bayesAdapter{db: db},
		BayesScope:      "global",
		BayesThreshold:  *cfg.Detection.BayesThreshold,
		BayesVocabGuess: cfg.Detection.BayesVocabGuess,
		BayesEnabled:    *cfg.Detection.BayesEnabled,
		Admins:          adminCache,
		FakeAdmin: detect.FakeAdminCfg{
			Enabled:        *cfg.Detection.FakeAdminEnabled,
			SuspiciousTags: cfg.Detection.FakeAdminSuspiciousTags,
			MaxDistance:    cfg.Detection.FakeAdminMaxDistance,
		},
		Blocklist:        blocklistSource,
		BlocklistEnabled: *cfg.Blocklist.Enabled,
	}
	handler.SetDecide(func(m domain.Message) (domain.Verdict, bool) {
		v, ok := cascade.Decide(m, false)
		if ok {
			reg.IncCounter("incidents_total", 1, "action", string(v.Action))
		}
		return v, ok
	})
	handler.SetEditedDecide(func(m domain.Message) (domain.Verdict, bool) {
		v, ok := cascade.Decide(m, true)
		if ok {
			reg.IncCounter("incidents_total", 1, "action", string(v.Action))
		}
		return v, ok
	})

	// Periodically sweep hist so stale duplicate/short-message events don't
	// accumulate forever. maxAge is a couple of windows wide so a burst
	// right at sweep time is never pruned mid-window; the ticker itself
	// (not maxAge) is what bounds memory growth.
	sweepMaxAge := 2 * behaviorCfg.DupWindow
	if w := 2 * behaviorCfg.ShortWindow; w > sweepMaxAge {
		sweepMaxAge = w
	}
	if sweepMaxAge <= 0 {
		sweepMaxAge = time.Hour
	}
	go func() {
		ticker := time.NewTicker(historySweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				hist.Sweep(sweepMaxAge)
			case <-ctx.Done():
				return
			}
		}
	}()

	if *cfg.Ops.DigestEnabled {
		go func() {
			t := time.NewTicker(cfg.Ops.DigestInterval.Duration())
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if err := ops.SendDigest(shutdownCtx, livePort, cfg.AdminChatID, db, time.Now().Unix()); err != nil {
						log.Printf("digest: %v", err)
					}
				}
			}
		}()
	}

	// Startup self-check: verify the bot's rights in every enabled chat once,
	// off the critical path, so misconfiguration is visible in the logs from
	// boot rather than only when an admin flips a right later.
	go func() {
		chats, err := db.ListEnabledChats()
		if err != nil {
			log.Printf("self-check: list chats: %v", err)
			return
		}
		for _, chat := range chats {
			select {
			case <-shutdownCtx.Done():
				return
			default:
			}
			selfCheck(chat)
		}
	}()

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
