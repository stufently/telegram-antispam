// Command tg-antispam is the single-process antispam bot.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/incident"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/version"
)

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
	defer seq.Wait()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := cfgStore.Watch(ctx, cfgPath); err != nil {
			log.Printf("config watcher stopped: %v", err)
		}
	}()

	var machine *incident.Machine // wired by later milestones via a live port
	handler := telegram.NewHandler(db, seq, cfgStore, machine)

	opts := []tgbot.Option{
		tgbot.WithAllowedUpdates([]string{
			"message", "edited_message", "callback_query",
			"chat_member", "my_chat_member", "message_reaction",
		}),
		tgbot.WithDefaultHandler(func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
			if update.Message == nil {
				return
			}
			handler.OnMessage(ctx, int64(update.ID), telegram.ToDomainMessage(update.Message))
		}),
	}

	b, err := tgbot.New(cfg.BotToken, opts...)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}
	log.Print("long polling started")
	b.Start(ctx)
}
