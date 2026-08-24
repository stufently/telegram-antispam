package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/stufently/telegram-antispam/internal/admin"
	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/train"
)

// runDecide implements the `decide` subcommand: it confirms one or more
// incidents as spam without going through the admin chat.
//
// Why it exists: a bot cannot press its own inline keyboard. Telegram only
// ever delivers a callback query from a user account, so anyone reviewing
// cards outside Telegram — an operator on the host, an agent watching the
// bot — has no way to answer a card at all. This is that way in.
//
// Only "confirm spam" is offered, and that is a deliberate ceiling rather
// than an unfinished feature. Confirming changes nothing in the source chat:
// the sanction was already applied, and the button only records the decision
// and trains the corpus. Every OTHER card action — false positive, lift,
// enforce, delete evidence — calls Telegram and either frees a muted user,
// sanctions a live one or destroys evidence. Those stay behind a human
// pressing a button, where a mistake has a face attached to it.
func runDecide(args []string, openDB func(string) (*store.DB, error)) (err error) {
	fs := flag.NewFlagSet("decide", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	dbPath := fs.String("db", os.Getenv("DB_PATH"), "path to the database (default $DB_PATH)")
	cfgPath := fs.String("config", os.Getenv("CONFIG_PATH"), "path to config.yaml, read only for detection.bayes_scope (default $CONFIG_PATH)")
	origin := fs.String("origin", "cli", "origin recorded on the decision sample")
	preview := fs.Bool("preview", false, "report what would be confirmed, change nothing")
	noTrain := fs.Bool("no-train", false, "record the decision but do not feed the message to the corpus")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return errors.New("no database path: pass -db or set DB_PATH")
	}
	// store.Open would CREATE a missing file, so a typo in -db would answer
	// "no such table: incidents" from a brand-new empty database instead of
	// saying the path is wrong — and leave the stray file behind.
	if _, statErr := os.Stat(*dbPath); statErr != nil {
		return fmt.Errorf("database %s: %w", *dbPath, statErr)
	}
	if fs.NArg() == 0 {
		return errors.New("no incident ids given")
	}

	ids := make([]int64, 0, fs.NArg())
	for _, a := range fs.Args() {
		id, convErr := strconv.ParseInt(a, 10, 64)
		if convErr != nil || id <= 0 {
			return fmt.Errorf("invalid incident id %q", a)
		}
		ids = append(ids, id)
	}

	// The scope decides WHICH corpus learns, so getting it wrong would teach
	// the wrong chats. It is read from the same config the running bot uses;
	// an unreadable config is fatal rather than a silent fall back to
	// "global", which would look like it worked. Not needed at all when
	// nothing will be learned.
	var scopeFor func(chatID int64) string
	if !*noTrain && !*preview {
		scopeFor, err = bayesScopeFromConfig(*cfgPath)
		if err != nil {
			return err
		}
	}

	db, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	// -no-train exists because "this is spam" and "this message is a good
	// example of spam" are different claims. A blocklist hit sanctions a
	// KNOWN id, and its message text can be perfectly ordinary — learning it
	// as spam teaches the corpus that ordinary text is spam. The decision is
	// still worth recording; the sample is not.
	var trainer func(chatID int64, label string, tokens []string) error
	if !*noTrain {
		trainer = func(chatID int64, label string, tokens []string) error {
			_, recErr := train.RecordTokens(db, scopeFor(chatID), label, *origin, tokens)
			return recErr
		}
	}

	var failed int
	for _, id := range ids {
		if decideErr := confirmOne(db, id, *origin, *preview, trainer); decideErr != nil {
			// One bad id must not abandon the rest: the ids come from a
			// review pass over several cards, and stopping halfway would
			// leave the operator guessing which ones landed.
			fmt.Fprintf(os.Stderr, "incident %d: %v\n", id, decideErr)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d incidents not confirmed", failed, len(ids))
	}
	return nil
}

// confirmOne claims the decision for a single incident and records it.
func confirmOne(db *store.DB, id int64, origin string, preview bool, trainer func(chatID int64, label string, tokens []string) error) error {
	inc, err := db.GetIncident(id)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("not found")
	}
	if err != nil {
		return fmt.Errorf("lookup: %w", err)
	}

	if preview {
		fmt.Printf("incident %d: would confirm spam (chat=%d action=%s dry_run=%t)\n",
			id, inc.ChatID, inc.Action, inc.DryRun)
		return nil
	}

	// Same single-decision claim the button takes, for the same reason: an
	// incident must not end up both confirmed and marked a false positive,
	// and a card someone presses later has to answer "already decided"
	// instead of learning the message twice.
	claimed, existing, err := db.RecordDecision(id, string(admin.ActConfirmSpam))
	if err != nil {
		return fmt.Errorf("claim decision: %w", err)
	}
	if !claimed {
		if existing != string(admin.ActConfirmSpam) {
			fmt.Printf("incident %d: already decided (%s), skipped\n", id, existing)
			return nil
		}
		// Already confirmed — but the claim is taken BEFORE the sample and
		// the training, so a crash (or a failed write) in between leaves a
		// decided incident that never learned anything. Its tokens are still
		// there in that case, because they are dropped only after a
		// successful train, so finishing the job now is safe and idempotent:
		// the sample insert ignores duplicates and a trained incident has no
		// tokens left to learn from.
		fmt.Printf("incident %d: already confirmed, completing\n", id)
	}

	res, err := admin.RecordConfirmation(db, inc, origin, trainer)
	if err != nil {
		if !claimed {
			// The decision was already there when this run started, so it is
			// not ours to withdraw: releasing it would reopen a card someone
			// else has answered.
			return fmt.Errorf("record confirmation: %w", err)
		}
		// Nothing reached Telegram, so our claim goes back and the card in
		// the admin chat stays answerable.
		if relErr := db.ReleaseDecision(id, string(admin.ActConfirmSpam)); relErr != nil {
			return fmt.Errorf("record confirmation: %w (claim not released: %v)", err, relErr)
		}
		return fmt.Errorf("record confirmation: %w", err)
	}

	if res.TrainErr != nil {
		// The decision is recorded and the tokens are still there, so a
		// re-run finishes the job. Reporting success here would tell a
		// script the corpus learned something it did not.
		fmt.Printf("incident %d: confirmed spam (chat=%d action=%s), NOT trained\n",
			id, inc.ChatID, inc.Action)
		return fmt.Errorf("confirmed but not trained: %w", res.TrainErr)
	}

	// -no-train has to be final, not merely skipped: the tokens outlive this
	// run, and the next plain `decide` on the same incident would take the
	// resume path and learn exactly what this flag refused.
	if trainer == nil {
		if err := db.DeleteIncidentTokens(id); err != nil {
			return fmt.Errorf("confirmed, but tokens not dropped: %w", err)
		}
	}

	fmt.Printf("incident %d: confirmed spam (chat=%d action=%s)%s\n",
		id, inc.ChatID, inc.Action, suffix(res.Reply))
	return nil
}

func suffix(trained string) string {
	if trained == "" {
		return ""
	}
	return ", " + trained
}

// bayesScopeFromConfig resolves the per-chat corpus scope the way the running
// bot does, so a decision made here trains exactly the corpus the message was
// scored against.
func bayesScopeFromConfig(path string) (func(chatID int64) string, error) {
	if path == "" {
		path = "config.yaml"
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return bayesScopeResolver(cfg), nil
}
