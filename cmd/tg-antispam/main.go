// Command tg-antispam is the single-process antispam bot.
package main

import (
	"context"
	"fmt"
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
	"github.com/stufently/telegram-antispam/internal/llm"
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
const (
	historySweepInterval = 5 * time.Minute
	// gracefulShutdownTimeout bounds the per-chat drain of work the
	// sequencer already accepted; backgroundShutdownTimeout separately bounds
	// the producers that stop ahead of it. They are separate budgets on
	// purpose: one phase overrunning must not silently consume the other's.
	gracefulShutdownTimeout   = 30 * time.Second
	backgroundShutdownTimeout = 10 * time.Second
)

// globalRateLimit and globalRateBurst bound total outbound Telegram calls
// across all chats; perChatRateLimit/perChatRateBurst bound calls to any one
// chat, well under Telegram's per-chat and global send limits.
const (
	globalRateLimit = 25
	globalRateBurst = 25
	perChatRateRPS  = 1
	perChatBurst    = 3

	// incidentTokenRetention bounds how long an unreviewed incident's
	// captured tokens are kept. Long enough that an admin can still act on
	// a digest from weeks ago; short enough that the database is not a
	// growing archive of message-derived content.
	incidentTokenRetention = 30 * 24 * time.Hour

	// telegramProbeInterval is how often the bot proves it can still reach
	// Telegram; telegramLivenessWindow is how long that may keep failing
	// before /livez reports unhealthy and the kubelet restarts the pod.
	//
	// The window is many probes wide ON PURPOSE. A restart fixes a wedged
	// process, not a Telegram outage, and it costs a full re-fetch of the
	// ~4.9M-entry blocklist — so a short window would turn somebody else's
	// downtime into our crash loop. Short outages are meant to be caught by
	// the metric (tg_antispam_telegram_probe_age_seconds), which alerts
	// long before this threshold.
	telegramProbeInterval  = 2 * time.Minute
	telegramLivenessWindow = 15 * time.Minute

	// updateDedupWindow is how many of the most recent update ids stay in
	// the dedup table. Telegram only ever redelivers updates that were not
	// acknowledged by the last getUpdates call — minutes of traffic at the
	// very most — so this window is orders of magnitude wider than the
	// duplicate it defends against, while keeping the table bounded.
	updateDedupWindow int64 = 100_000
)

// priorityFor maps a Port method name to its queue priority: destructive
// moderation calls (delete/ban/restrict) jump ahead of notifications and
// bookkeeping.
func priorityFor(method string) queue.Priority {
	switch method {
	case "DeleteMessages", "BanMember", "UnbanMember", "RestrictMember", "UnrestrictMember", "BanSenderChat":
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

// chatScope is the per-chat Bayes corpus name. Prefixed so it can never
// collide with the shared "global" scope or with a future named one.
func chatScope(chatID int64) string { return fmt.Sprintf("chat:%d", chatID) }

// TokenCounts reads a scope's token counts, LAYERED on the shared corpus
// when the scope is a per-chat one.
//
// The layering is the whole design: the seeded corpus is imported under
// "global", so a chat scope on its own would be empty and every message in
// it unscoreable. Summing the two makes per-chat training a refinement of
// the shared corpus rather than a replacement for it — a chat starts out
// identical to global and diverges only as far as moderators push it.
func (a bayesAdapter) TokenCounts(scope string, tokens []string) (map[string]int, map[string]int, error) {
	spam, ham, err := a.db.TokenCounts(scope, tokens)
	if err != nil || scope == string(domain.ScopeGlobal) {
		return spam, ham, err
	}
	gSpam, gHam, err := a.db.TokenCounts(string(domain.ScopeGlobal), tokens)
	if err != nil {
		return nil, nil, err
	}
	for tok, n := range gSpam {
		spam[tok] += n
	}
	for tok, n := range gHam {
		ham[tok] += n
	}
	return spam, ham, nil
}

func (a bayesAdapter) Totals(scope string) (detect.BayesCounts, error) {
	sd, hd, st, ht, err := a.db.BayesTotals(scope)
	if err != nil {
		return detect.BayesCounts{}, err
	}
	counts := detect.BayesCounts{SpamDocs: sd, HamDocs: hd, SpamTokenTotal: st, HamTokenTotal: ht}
	if scope == string(domain.ScopeGlobal) {
		return counts, nil
	}
	// Same layering as TokenCounts: the class priors must describe the same
	// corpus the token counts came from, or the score is nonsense.
	gsd, ghd, gst, ght, err := a.db.BayesTotals(string(domain.ScopeGlobal))
	if err != nil {
		return detect.BayesCounts{}, err
	}
	counts.SpamDocs += gsd
	counts.HamDocs += ghd
	counts.SpamTokenTotal += gst
	counts.HamTokenTotal += ght
	return counts, nil
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

	// Handle backup subcommand if present. Kept before any logging: the
	// snapshot goes to stdout, and a stray log line there would corrupt it
	// (log writes to stderr, but the ordering makes the intent explicit).
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		if err := runBackup(os.Args[2:], func(p string) (*store.DB, error) {
			return store.Open(p)
		}); err != nil {
			log.Fatalf("backup: %v", err)
		}
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

	// Warn (never fail) about config keys this binary does not know: yaml.v3
	// drops them silently, so a values file that runs ahead of the image looks
	// like it applied and quietly does nothing. Logged here so the deploy
	// pipeline can grep for it.
	if raw, readErr := os.ReadFile(cfgPath); readErr == nil {
		if unknown := config.UnknownKeys(raw); unknown != nil {
			log.Printf("config: UNKNOWN KEYS IGNORED by this version — %v", unknown)
		}
	}

	db, err := store.Open(os.Getenv("DB_PATH"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	seq := telegram.NewSequencer()

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	// workCtx outlives signalCtx during the bounded drain. Polling and
	// background producers stop on signalCtx; already-accepted sequencer jobs
	// and the outbound dispatcher keep workCtx until they finish or the grace
	// deadline expires.
	workCtx, stopWork := context.WithCancel(context.Background())
	defer stopWork()

	var background sync.WaitGroup
	startBackground := func(fn func()) {
		background.Add(1)
		go func() {
			defer background.Done()
			fn()
		}()
	}

	startBackground(func() {
		if err := cfgStore.Watch(signalCtx, cfgPath); err != nil {
			log.Printf("config watcher stopped: %v", err)
		}
	})

	// reg is the M7 metrics registry: populated at the wiring seams below
	// (the default-handler switch, the decide hook, the blocklist gauge)
	// and served by the ops HTTP server started further down, once cfg.Ops
	// is known to be fully defaulted.
	reg := ops.NewRegistry()
	// health backs /livez. Created before the ops server so the endpoint is
	// wired from the first request; the probe that feeds it starts once
	// LivePort exists (further down).
	health := ops.NewHealth(telegramLivenessWindow, time.Now())
	if *cfg.Ops.MetricsEnabled {
		startBackground(func() {
			if err := ops.NewServer(cfg.Ops.MetricsAddr, reg, health).Run(signalCtx); err != nil {
				log.Printf("ops server: %v", err)
			}
		})
	}

	// The dispatcher owns every outbound telegram.Port call (long-polling
	// transport is library-owned): global + per-chat rate limiting, priority
	// ordering, and 429 retry.
	limiters := newChatLimiters()
	disp := queue.NewDispatcher(rate.NewLimiter(rate.Limit(globalRateLimit), globalRateBurst), limiters.get)
	dispDone := make(chan struct{})
	go func() {
		disp.Run(workCtx)
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
		adminCache      *telegram.AdminCache
		selfCheck       func(context.Context, int64)
	)

	opts := []tgbot.Option{
		// Bot.New otherwise performs a direct GetMe before LivePort exists.
		// Skip it so the first identity lookup is routed through LivePort and
		// therefore receives the same rate limiting and 429 retry as every
		// other telegram.Port call.
		tgbot.WithSkipGetMe(),
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
		tgbot.WithDefaultHandler(func(updateCtx context.Context, b *tgbot.Bot, update *models.Update) {
			switch {
			case update.Message != nil:
				reg.IncCounter("tg_antispam_updates_total", 1, "kind", "message")
				handler.OnMessage(updateCtx, update.ID, telegram.ToDomainMessage(update.Message))
			case update.EditedMessage != nil:
				reg.IncCounter("tg_antispam_updates_total", 1, "kind", "edited")
				handler.OnEditedMessage(updateCtx, update.ID, telegram.ToDomainMessage(update.EditedMessage))
			case update.CallbackQuery != nil:
				reg.IncCounter("tg_antispam_updates_total", 1, "kind", "callback")
				cb := update.CallbackQuery
				// Offload onto the sequencer: WithNotAsyncHandlers runs this
				// inline on the single poll-consumer goroutine, and
				// adminHandler.Handle does DB + GetChatAdministrators
				// (network) + AnswerCallback work that would otherwise stall
				// polling. All admin callbacks share one bucket (cfg's admin
				// chat id) — fine since they're low volume; seq.Wait() at
				// shutdown drains this job like any other. Use workCtx, not the
				// per-update context, so an accepted job remains live during the
				// bounded drain after polling stops.
				seq.Submit(cfgStore.Current().AdminChatID, func() {
					// The offending message's tokens were persisted when the
					// incident was created (store.SaveIncidentTokens), so the
					// handler loads them itself — nothing message-shaped has
					// to survive on the callback payload, which carries only
					// where the button sits.
					var adminChatID int64
					var messageID int
					if cb.Message.Message != nil {
						adminChatID = cb.Message.Message.Chat.ID
						messageID = cb.Message.Message.ID
					}
					err := adminHandler.Handle(workCtx, admin.Callback{
						ID:          cb.ID,
						Data:        cb.Data,
						PresserID:   cb.From.ID,
						AdminChatID: adminChatID,
						MessageID:   messageID,
					})
					if err != nil {
						log.Printf("admin callback: %v", err)
					}
				})
			case update.ChatMember != nil:
				reg.IncCounter("tg_antispam_updates_total", 1, "kind", "chat_member")
				cm := update.ChatMember
				mem := telegram.MemberFromChatMember(cm.NewChatMember)
				// Only a change that touches the admin roster invalidates it.
				// chat_member also fires for every ordinary join, leave, and
				// restriction, and dropping the cache on those would turn the
				// TTL cache into a per-event GetChatAdministrators during a
				// raid — and stretch the windows where a failing lookup has
				// nothing cached to fall back on. Invalidate on the inline
				// consumer, before later updates from this chat can be
				// submitted, then let the sequenced watcher refetch as needed.
				if isAdminStatus(telegram.MemberFromChatMember(cm.OldChatMember).Status) || isAdminStatus(mem.Status) {
					adminCache.Invalidate(cm.Chat.ID)
				}
				ev := watch.MemberEvent{ChatID: cm.Chat.ID, UserID: mem.UserID, Username: mem.Username, DisplayName: mem.DisplayName}
				seq.Submit(cm.Chat.ID, func() {
					if memberWatcher != nil {
						if err := memberWatcher.Observe(workCtx, ev); err != nil {
							log.Printf("member watch: %v", err)
						}
					}
				})
			case update.MessageReaction != nil:
				reg.IncCounter("tg_antispam_updates_total", 1, "kind", "reaction")
				mr := update.MessageReaction
				if mr.User != nil { // only user-attributed reactions (skip anonymous/actor-chat)
					ev := watch.ReactionEvent{ChatID: mr.Chat.ID, MessageID: mr.MessageID, UserID: mr.User.ID, Added: len(mr.NewReaction) > len(mr.OldReaction)}
					seq.Submit(mr.Chat.ID, func() {
						if reactionCleaner != nil {
							if err := reactionCleaner.Observe(workCtx, ev); err != nil {
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
				reg.IncCounter("tg_antispam_updates_total", 1, "kind", "my_chat_member")
				chat := update.MyChatMember.Chat.ID
				// The bot's own promotion/demotion changes this chat's
				// administrator roster too, so the cached list is now wrong
				// for the §4 immunity gate and the fake-admin matcher alike.
				// Invalidate on the inline consumer, exactly as chat_member
				// does, rather than waiting out the TTL.
				adminCache.Invalidate(chat)
				seq.Submit(chat, func() {
					if selfCheck != nil {
						selfCheck(workCtx, chat)
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
	// Explicit token/connectivity probe. Bot.New ran WithSkipGetMe (above), so
	// nothing has validated the token yet; without this a revoked or
	// mistyped token would not fail startup at all — b.Start would sit in
	// getUpdates retrying every 5s while /healthz and /metrics reported a
	// perfectly healthy container. Fatal on failure, matching Bot.New's own
	// behavior before the identity lookup moved onto the dispatcher. The
	// resolved id is cached on livePort, so self-checks skip GetMe.
	selfID, err := livePort.Self(signalCtx)
	if err != nil {
		log.Fatalf("bot identity (GetMe): %v", err)
	}
	log.Printf("bot identity resolved: id=%d", selfID)

	// Liveness probe: a periodic GetMe down the same path every other call
	// takes (rate limiter, dispatcher, HTTP client). Update traffic cannot
	// serve this purpose — a quiet night in every chat is normal — so the
	// bot asks Telegram a question it always knows the answer to.
	startBackground(func() {
		t := time.NewTicker(telegramProbeInterval)
		defer t.Stop()
		for {
			select {
			case <-signalCtx.Done():
				return
			case <-t.C:
				pctx, cancel := context.WithTimeout(workCtx, telegramProbeInterval)
				err := livePort.Ping(pctx)
				cancel()
				if err != nil {
					log.Printf("telegram probe failed: %v", err)
				} else {
					health.Beat(time.Now())
				}
				reg.SetGauge("tg_antispam_telegram_probe_age_seconds", health.Age(time.Now()).Seconds())
			}
		}
	})
	// selfCheck reads the bot's admin rights in one chat and logs any missing
	// rights or hazards (spec §13). Best-effort: a transient read error is
	// logged and dropped, never fatal.
	selfCheck = func(ctx context.Context, chat int64) {
		warnings, err := selfcheck.Check(ctx, livePort, chat)
		if err != nil {
			log.Printf("self-check chat %d: %v", chat, err)
			return
		}
		if len(warnings) == 0 {
			// Log the clean result too, not just problems. Adding a bot to a
			// chat produces two my_chat_member events — joined as a member,
			// then promoted — so the log otherwise ends on "bot is not an
			// administrator" and stays that way, with nothing to say the
			// promotion landed. That reads as a broken deploy when everything
			// is fine.
			log.Printf("self-check chat %d: rights ok", chat)
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
	handler.SetContext(workCtx)
	adminHandler = admin.NewHandler(livePort, db, operatorSet(cfg))
	// bayesScopeFor decides whose corpus a chat is scored against. In
	// per_chat mode the chat's own scope is ADDITIVE: bayesAdapter reads it
	// on top of the shared corpus, so a chat nobody has trained yet scores
	// exactly as it did under "global". Without that layering, switching
	// modes would silently disarm Bayes in every chat until moderators had
	// rebuilt a corpus by hand.
	bayesScopeFor := func(chatID int64) string {
		if cfg.Detection.BayesScope != config.BayesScopePerChat {
			return string(domain.ScopeGlobal)
		}
		return chatScope(chatID)
	}

	// Moderator feedback trains the same corpus the message was SCORED
	// against, so a confirm/false-positive press in the crypto chat does not
	// teach the hookah chats that "куплю usdt" is fine.
	adminHandler.SetTrainer(func(chatID int64, label string, tokens []string) error {
		_, err := train.RecordTokens(db, bayesScopeFor(chatID), label, "user", tokens)
		return err
	})

	// adminCache is the M5 fake-admin detector's detect.AdminSource: a
	// TTL-cached wrapper over GetChatAdministrators so both the cascade
	// (message-time check) and the member watcher (join/rename-time check)
	// share one cache per chat instead of hitting Telegram on every event.
	adminCache = telegram.NewAdminCache(livePort, time.Duration(cfg.Detection.AdminCacheTTLSeconds)*time.Second)
	adminCache.SetContext(workCtx)
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
	// risk a nil-receiver call. The syncer stops with the background producers
	// on signalCtx.
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
		startBackground(func() { bl.Run(signalCtx) })
		blocklistSource = bl

		// tg_antispam_blocklist_size gauge: sampled on a ticker rather than pushed from
		// the syncer itself, so the ops package stays decoupled from
		// internal/blocklist (see the M7 brief's "keep instrumentation
		// minimal, don't thread the registry deep" guidance).
		startBackground(func() {
			// Sample once before the loop: a ticker-only gauge means /metrics
			// carries no tg_antispam_* series at all for the first minute
			// after boot, so an early scrape (or a deploy-time smoke check)
			// sees an empty-looking service and cannot tell it from a broken
			// one. The first sample may legitimately be 0 while the bootstrap
			// fetch is still running; the alert on it waits 30m for exactly
			// that reason.
			reg.SetGauge("tg_antispam_blocklist_size", float64(bl.Len()))
			t := time.NewTicker(time.Minute)
			defer t.Stop()
			for {
				select {
				case <-signalCtx.Done():
					return
				case <-t.C:
					reg.SetGauge("tg_antispam_blocklist_size", float64(bl.Len()))
				}
			}
		})
	}

	// Sequencer health. Both counters are silent failures by construction:
	// a dropped job is an update already marked seen in SQLite (so it is
	// never reprocessed) that simply never got moderated, and a contained
	// panic is a bug that no longer takes the process down — which is
	// exactly what makes it easy to miss. Sampled on a ticker like the
	// blocklist gauge, so internal/telegram keeps knowing nothing about the
	// metrics registry.
	startBackground(func() {
		sample := func() {
			reg.SetGauge("tg_antispam_jobs_dropped", float64(seq.Dropped()))
			reg.SetGauge("tg_antispam_jobs_panicked", float64(seq.Panicked()))
		}
		sample()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-signalCtx.Done():
				return
			case <-t.C:
				sample()
			}
		}
	})

	// Optional borderline LLM stage (§5.4): built only when explicitly enabled
	// in config (privacy: external calls are opt-in). When off, the judge is
	// nil and the cascade's borderline band stays 0, so no message text ever
	// leaves the process.
	var llmJudge *llm.Judge
	var bayesBorderlineBand float64
	if *cfg.LLM.Enabled && len(cfg.LLM.Providers) > 0 {
		provs := make([]llm.Provider, 0, len(cfg.LLM.Providers))
		for _, pc := range cfg.LLM.Providers {
			switch pc.Kind {
			case "openai":
				provs = append(provs, llm.OpenAI{
					APIKey:      pc.APIKey,
					Model:       pc.Model,
					Prompt:      cfg.LLM.Prompt,
					Temperature: cfg.LLM.Temperature,
					MaxTokens:   cfg.LLM.MaxTokens,
				})
			case "anthropic":
				provs = append(provs, llm.Anthropic{
					APIKey:      pc.APIKey,
					Model:       pc.Model,
					Prompt:      cfg.LLM.Prompt,
					Temperature: cfg.LLM.Temperature,
					MaxTokens:   cfg.LLM.MaxTokens,
				})
			}
		}
		llmJudge = &llm.Judge{Providers: provs, Policy: llm.Policy(cfg.LLM.Policy)}
		bayesBorderlineBand = cfg.LLM.BorderlineBand
		log.Printf("LLM borderline stage enabled: %d provider(s), policy=%s, band=%.3g",
			len(provs), cfg.LLM.Policy, bayesBorderlineBand)
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
		Behavior:            behaviorCfg,
		TrustThreshold:      *cfg.Detection.TrustThreshold,
		DefaultAction:       cfg.Action,
		DefaultScope:        domain.ScopeGlobal,
		Bayes:               bayesAdapter{db: db},
		BayesScope:          bayesScopeFor,
		BayesThreshold:      *cfg.Detection.BayesThreshold,
		BayesVocabGuess:     cfg.Detection.BayesVocabGuess,
		BayesEnabled:        *cfg.Detection.BayesEnabled,
		BayesBorderlineBand: bayesBorderlineBand,
		Admins:              adminCache,
		FakeAdmin: detect.FakeAdminCfg{
			Enabled:        *cfg.Detection.FakeAdminEnabled,
			SuspiciousTags: cfg.Detection.FakeAdminSuspiciousTags,
			MaxDistance:    cfg.Detection.FakeAdminMaxDistance,
			MinFuzzyLen:    cfg.Detection.FakeAdminMinFuzzyLen,
		},
		Blocklist:        blocklistSource,
		BlocklistEnabled: *cfg.Blocklist.Enabled,
	}
	llmTimeout := cfg.LLM.HTTPTimeout.Duration()
	// decideWith runs the pure cascade, then — for a non-actionable
	// "bayes_borderline" result and only when the LLM stage is enabled —
	// consults the LLM judge. A spam consensus upgrades the verdict to an
	// actionable "llm" incident. Runs on the per-chat sequencer worker, so a
	// blocking LLM call delays only that chat's queue, never all updates.
	decideWith := func(m domain.Message, edited bool) (domain.Verdict, bool) {
		v, ok := cascade.Decide(m, edited)
		if !ok && llmJudge != nil && hasBorderline(v) {
			cctx, cancel := context.WithTimeout(workCtx, llmTimeout)
			out := llmJudge.Adjudicate(cctx, m.Text, cfg.LLM.PromptFor(m.ChatID))
			cancel()
			// A failed call is counted as "error", never as "ham". The stage
			// is fail-open, so an expired key, an exhausted quota or a
			// timing-out endpoint yields exactly the same verdict as a model
			// answering HAM — and this is the only stage that catches spam
			// the Bayes corpus has never seen (the crypto euphemisms tg-spam
			// needed a custom prompt for). Without a distinct label, the paid
			// detector could die and every dashboard would keep showing
			// healthy "ham" checks.
			// ONE sample per adjudication, never one per provider: with
			// policy "any" and answers [spam, ham] a per-provider count
			// recorded two spam checks for a single message, and the
			// metric stopped meaning "messages the LLM judged".
			// "error" is reserved for an adjudication that got no answer
			// at all — that is the one with no verdict behind it.
			if out.Failed > 0 {
				log.Printf("llm: %d/%d provider(s) failed: %v", out.Failed, out.Total, out.Err)
				reg.IncCounter("tg_antispam_llm_provider_failures_total", float64(out.Failed))
			}
			if out.Failed >= out.Total {
				reg.IncCounter("tg_antispam_llm_checks_total", 1, "result", "error")
			} else {
				reg.IncCounter("tg_antispam_llm_checks_total", 1, "result", boolLabel(out.Spam))
			}
			if out.Spam {
				v = domain.Verdict{
					Action:     cfg.Action,
					Scope:      domain.ScopeGlobal,
					Confidence: 1.0,
					Signals:    []domain.Signal{{Name: "llm"}},
					Reason:     "llm",
				}
				ok = true
			}
		}
		if ok {
			reg.IncCounter("tg_antispam_incidents_total", 1, "action", string(v.Action))
		}
		return v, ok
	}
	handler.SetDecide(func(m domain.Message) (domain.Verdict, bool) { return decideWith(m, false) })
	handler.SetEditedDecide(func(m domain.Message) (domain.Verdict, bool) { return decideWith(m, true) })

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
	startBackground(func() {
		ticker := time.NewTicker(historySweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				hist.Sweep(sweepMaxAge)
			case <-signalCtx.Done():
				return
			}
		}
	})

	// Retention for captured incident tokens: incidents nobody ever pressed
	// a button on would otherwise keep message-derived content forever. Runs
	// once at startup and then daily; failures are logged, never fatal.
	startBackground(func() {
		prune := func() {
			n, err := db.PruneIncidentTokens(incidentTokenRetention)
			if err != nil {
				log.Printf("prune incident tokens: %v", err)
			} else if n > 0 {
				log.Printf("pruned %d incident token row(s) older than %s", n, incidentTokenRetention)
			}
			// Same ticker, same reason: the update dedup table grew by one
			// row per update forever, on a volume that also holds the
			// corpus and every incident.
			if n, err := db.PruneUpdates(updateDedupWindow); err != nil {
				log.Printf("prune updates: %v", err)
			} else if n > 0 {
				log.Printf("pruned %d dedup row(s) beyond the newest %d update ids", n, updateDedupWindow)
			}
		}
		prune()
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-signalCtx.Done():
				return
			case <-t.C:
				prune()
			}
		}
	})

	if *cfg.Ops.DigestEnabled {
		startBackground(func() {
			t := time.NewTicker(cfg.Ops.DigestInterval.Duration())
			defer t.Stop()
			for {
				select {
				case <-signalCtx.Done():
					return
				case <-t.C:
					if err := ops.SendDigest(signalCtx, livePort, cfg.AdminChatID, db, time.Now().Unix()); err != nil {
						log.Printf("digest: %v", err)
					}
				}
			}
		})
	}

	// Startup self-check: verify the bot's rights in every enabled chat once,
	// off the critical path, so misconfiguration is visible in the logs from
	// boot rather than only when an admin flips a right later.
	startBackground(func() {
		chats, err := db.ListEnabledChats()
		if err != nil {
			log.Printf("self-check: list chats: %v", err)
			return
		}
		// The stored list only holds chats that have already produced an
		// update, because that is when a chat is registered. A configured
		// chat the bot was added to but where nobody has spoken yet was
		// therefore never checked — and that is precisely the chat where
		// missing delete/restrict rights go unnoticed, since there is no
		// traffic to reveal them. In allowlist mode the full set is known
		// up front, so check it.
		seen := make(map[int64]bool, len(chats))
		for _, chat := range chats {
			seen[chat] = true
		}
		for _, chat := range cfg.Chats.Allowlist {
			if !seen[chat] {
				seen[chat] = true
				chats = append(chats, chat)
			}
		}
		for _, chat := range chats {
			select {
			case <-signalCtx.Done():
				return
			default:
			}
			selfCheck(signalCtx, chat)
		}
	})

	log.Print("long polling started")
	b.Start(signalCtx) // blocks until signalCtx is cancelled (SIGINT/SIGTERM)

	// Graceful shutdown, in dependency order. The dispatcher stays alive on
	// workCtx while producers stop and accepted per-chat work drains. A hard
	// deadline cancels workCtx so a stuck network call or repeated 429 cannot
	// block process termination forever.
	if !waitWithin(&background, backgroundShutdownTimeout) {
		log.Printf("background producers still running after %s; proceeding with shutdown", backgroundShutdownTimeout)
	}
	// Arm the drain deadline only now. It exists to bound the per-chat work
	// below; arming it before the producers stopped would let a slow producer
	// eat the drain's entire budget and cancel workCtx before any accepted
	// work had a chance to finish — the opposite of the intent.
	shutdownTimer := time.AfterFunc(gracefulShutdownTimeout, func() {
		log.Printf("graceful shutdown timed out after %s; canceling accepted work", gracefulShutdownTimeout)
		stopWork()
	})
	handler.Stop()
	seq.Wait()
	shutdownTimer.Stop()
	stopWork()
	<-dispDone
}

// isAdminStatus reports whether a chat_member status is one that puts the
// member on the administrator roster the admin cache holds.
func isAdminStatus(status string) bool {
	return status == "administrator" || status == "creator"
}

// waitWithin waits for wg, reporting false if d elapses first. Overrunning
// goroutines are not abandoned — they keep running against an already
// cancelled signalCtx — but shutdown stops blocking on them, so a producer
// that refuses to stop cannot hold the process open indefinitely or eat the
// drain budget that follows.
func waitWithin(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// operatorSet builds the global-operator set admin.NewHandler expects from
// chats.operators. An empty set is a working configuration — administrators of
// the source chat can still act — but it is the wrong default for a shared
// admin chat: a moderator who watches the evidence feed without being an admin
// of that particular group would get "not authorized".
func operatorSet(cfg *config.Config) map[int64]bool {
	ops := make(map[int64]bool, len(cfg.Chats.Operators))
	for _, id := range cfg.Chats.Operators {
		ops[id] = true
	}
	return ops
}

// hasBorderline reports whether a verdict carries the cascade's
// "bayes_borderline" signal, marking it a candidate for LLM adjudication (§5.4).
func hasBorderline(v domain.Verdict) bool {
	for _, s := range v.Signals {
		if s.Name == "bayes_borderline" {
			return true
		}
	}
	return false
}

// boolLabel maps a spam decision to a stable metric label value.
func boolLabel(spam bool) string {
	if spam {
		return "spam"
	}
	return "ham"
}
