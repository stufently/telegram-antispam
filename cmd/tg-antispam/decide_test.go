package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

// decideEnv builds a migrated database plus a config file the subcommand can
// read, and returns the two paths.
func decideEnv(t *testing.T, extraConfig string) (dbPath, cfgPath string, db *store.DB) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "t.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	cfgPath = filepath.Join(dir, "config.yaml")
	cfg := "bot_token: \"12345:AA\"\nadmin_chat_id: -1009999\naction: delete_mute\nchats:\n  mode: auto\n" + extraConfig
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dbPath, cfgPath, db
}

func openDB(p string) (*store.DB, error) { return store.Open(p) }

// pendingWithTokens creates one live incident carrying tokens to learn from.
func pendingWithTokens(t *testing.T, db *store.DB, chatID int64, tokens []string) int64 {
	t.Helper()
	id, _, err := db.InsertPending(chatID, 5, 77, 0, false, domain.Verdict{Action: domain.ActionDeleteMute})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIncidentTokens(id, tokens); err != nil {
		t.Fatal(err)
	}
	return id
}

func decisionOf(t *testing.T, db *store.DB, id int64) string {
	t.Helper()
	var d string
	if err := db.Read().QueryRow("SELECT decision FROM incidents WHERE id=?", id).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func tokenRowsFor(t *testing.T, db *store.DB, id int64) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM incident_tokens WHERE incident_id=?", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDecideConfirmsAndTrains is the whole point of the subcommand: a bot
// cannot press its own button, so this path must land the same two effects
// the button lands — the decision and the corpus sample.
func TestDecideConfirmsAndTrains(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "")
	id := pendingWithTokens(t, db, -100777, []string{"free", "casino"})

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "1"}, openDB); err != nil {
		t.Fatal(err)
	}

	if got := decisionOf(t, db, id); got != "confirm" {
		t.Fatalf("decision = %q, want confirm", got)
	}
	docs, _, _, _, err := db.BayesTotals(string(domain.ScopeGlobal))
	if err != nil {
		t.Fatal(err)
	}
	if docs != 1 {
		t.Fatalf("global corpus learned %d docs, want 1", docs)
	}
	if n := tokenRowsFor(t, db, id); n != 0 {
		t.Fatalf("tokens kept after a successful train: %d rows", n)
	}
}

// TestDecideNoTrainRecordsWithoutLearning: confirming that a sanction was
// right is not the same claim as "this text is a good example of spam" — a
// blocklist hit sanctions a known id and its text may be ordinary.
func TestDecideNoTrainRecordsWithoutLearning(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "")
	id := pendingWithTokens(t, db, -100777, []string{"hello", "there"})

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "-no-train", "1"}, openDB); err != nil {
		t.Fatal(err)
	}

	if got := decisionOf(t, db, id); got != "confirm" {
		t.Fatalf("decision = %q, want confirm", got)
	}
	docs, _, _, _, err := db.BayesTotals(string(domain.ScopeGlobal))
	if err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Fatalf("corpus learned %d docs with -no-train", docs)
	}
	// The flag has to be FINAL: leaving the tokens behind would let the very
	// next plain `decide` take the resume path and learn what was refused.
	if n := tokenRowsFor(t, db, id); n != 0 {
		t.Fatalf("tokens survived -no-train: %d rows", n)
	}

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "1"}, openDB); err != nil {
		t.Fatal(err)
	}
	docs, _, _, _, err = db.BayesTotals(string(domain.ScopeGlobal))
	if err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Fatalf("a re-run learned %d docs that -no-train had refused", docs)
	}
}

// TestDecidePreviewChangesNothing: -preview is for reading a card before
// answering it, so it must not take the decision it is describing.
func TestDecidePreviewChangesNothing(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "")
	id := pendingWithTokens(t, db, -100777, []string{"free", "casino"})

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "-preview", "1"}, openDB); err != nil {
		t.Fatal(err)
	}

	if got := decisionOf(t, db, id); got != "" {
		t.Fatalf("preview recorded a decision: %q", got)
	}
	if n := tokenRowsFor(t, db, id); n != 1 {
		t.Fatalf("preview dropped tokens: %d rows", n)
	}
}

// TestDecideLeavesAnotherDecisionAlone: one decision per incident. A card
// already marked a false positive must not be flipped to spam from here.
func TestDecideLeavesAnotherDecisionAlone(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "")
	id := pendingWithTokens(t, db, -100777, []string{"free", "casino"})
	if claimed, _, err := db.RecordDecision(id, "fp"); err != nil || !claimed {
		t.Fatalf("setup claim: claimed=%t err=%v", claimed, err)
	}

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "1"}, openDB); err != nil {
		t.Fatal(err)
	}

	if got := decisionOf(t, db, id); got != "fp" {
		t.Fatalf("decision = %q, want the untouched fp", got)
	}
	docs, _, _, _, err := db.BayesTotals(string(domain.ScopeGlobal))
	if err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Fatalf("learned %d docs from an incident it must not have touched", docs)
	}
}

// TestDecideCompletesAnInterruptedConfirm: the claim is taken before the
// sample and the training, so a crash in between leaves a decided incident
// that learned nothing. Re-running must finish the job rather than report
// "already decided" and walk away.
func TestDecideCompletesAnInterruptedConfirm(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "")
	id := pendingWithTokens(t, db, -100777, []string{"free", "casino"})
	if claimed, _, err := db.RecordDecision(id, "confirm"); err != nil || !claimed {
		t.Fatalf("setup claim: claimed=%t err=%v", claimed, err)
	}

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "1"}, openDB); err != nil {
		t.Fatal(err)
	}

	docs, _, _, _, err := db.BayesTotals(string(domain.ScopeGlobal))
	if err != nil {
		t.Fatal(err)
	}
	if docs != 1 {
		t.Fatalf("interrupted confirm not completed: %d docs learned", docs)
	}
}

// TestDecidePerChatScopeTrainsThatChat: a decision must train the corpus the
// message was SCORED against, or a confirm in one chat teaches every other.
func TestDecidePerChatScopeTrainsThatChat(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "detection:\n  bayes_scope: per_chat\n")
	pendingWithTokens(t, db, -100777, []string{"free", "casino"})

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "1"}, openDB); err != nil {
		t.Fatal(err)
	}

	docs, _, _, _, err := db.BayesTotals(chatScope(-100777))
	if err != nil {
		t.Fatal(err)
	}
	if docs != 1 {
		t.Fatalf("chat corpus learned %d docs, want 1", docs)
	}
	globalDocs, _, _, _, err := db.BayesTotals(string(domain.ScopeGlobal))
	if err != nil {
		t.Fatal(err)
	}
	if globalDocs != 0 {
		t.Fatalf("shared corpus learned %d docs it was not scored against", globalDocs)
	}
}

// TestDecideReportsMissingIncidentAndKeepsGoing: the ids come from one review
// pass over several cards, so a wrong one must not abandon the others — but
// it must still be visible in the exit status.
func TestDecideReportsMissingIncidentAndKeepsGoing(t *testing.T) {
	dbPath, cfgPath, db := decideEnv(t, "")
	id := pendingWithTokens(t, db, -100777, []string{"free", "casino"})

	err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "999", "1"}, openDB)
	if err == nil {
		t.Fatal("a missing incident must fail the run")
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("error does not say what failed: %v", err)
	}
	if got := decisionOf(t, db, id); got != "confirm" {
		t.Fatalf("the good id was skipped: decision = %q", got)
	}
}

// TestDecideRefusesAMissingDatabase: store.Open would create the file, so a
// typo in -db would otherwise answer "no such table" from an empty database
// it just made.
func TestDecideRefusesAMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.db")

	err := runDecide([]string{"-db", missing, "1"}, openDB)
	if err == nil {
		t.Fatal("expected an error for a missing database")
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Fatal("a missing -db path must not be created")
	}
}

// TestDecideRejectsBadArguments: no ids at all, and a non-numeric id.
func TestDecideRejectsBadArguments(t *testing.T) {
	dbPath, cfgPath, _ := decideEnv(t, "")

	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath}, openDB); err == nil {
		t.Fatal("expected an error when no incident ids are given")
	}
	if err := runDecide([]string{"-db", dbPath, "-config", cfgPath, "abc"}, openDB); err == nil {
		t.Fatal("expected an error for a non-numeric incident id")
	}
}

// TestUnknownSubcommandIsRefused: on 2026-08-24 a `kubectl exec ...
// /tg-antispam decide` against a build that had no such subcommand fell
// through into main and started a SECOND long-polling bot on the same token.
// Telegram handed the updates to the intruder and the real pod logged
// "terminated by other getUpdates request" until it was restarted.
func TestUnknownSubcommandIsRefused(t *testing.T) {
	if got := unknownSubcommand([]string{"tg-antispam", "decide"}); got != "" {
		t.Fatalf("known subcommand rejected: %q", got)
	}
	for _, known := range []string{"import", "backup"} {
		if got := unknownSubcommand([]string{"tg-antispam", known}); got != "" {
			t.Fatalf("known subcommand %q rejected: %q", known, got)
		}
	}
	if got := unknownSubcommand([]string{"tg-antispam"}); got != "" {
		t.Fatalf("the bot's own no-argument start was rejected: %q", got)
	}
	if got := unknownSubcommand([]string{"tg-antispam", "decid"}); got != "decid" {
		t.Fatalf("a typo must be refused, got %q", got)
	}
}
