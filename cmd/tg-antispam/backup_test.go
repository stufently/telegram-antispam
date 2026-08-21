package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/train"
)

// TestBackupProducesAReadableDatabase: the backup must be a database, not a
// file-shaped copy of one. It is taken while the source is open, which is
// the whole point — the bot never stops for it.
func TestBackupProducesAReadableDatabase(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")

	live, err := store.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Close() }()
	if err := live.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := live.InsertPending(-100, 5, 7, 0, false, domain.Verdict{Action: domain.ActionBan}); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out.db")
	if err := runBackup([]string{"-db", src, dst}, store.Open); err != nil {
		t.Fatal(err)
	}

	restored, err := store.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	row, err := restored.GetIncident(1)
	if err != nil {
		t.Fatalf("reading the restored copy: %v", err)
	}
	if row.ChatID != -100 || row.UserID != 7 {
		t.Fatalf("restored incident = %+v, want the source row", row)
	}
}

// TestBackupToStdoutIsAnSQLiteFile pins the CI path: `kubectl exec ... --
// /tg-antispam backup -` redirected to a file. A truncated or log-polluted
// stream would still create a file, so the header is checked explicitly.
func TestBackupToStdoutIsAnSQLiteFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	live, err := store.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Migrate(); err != nil {
		t.Fatal(err)
	}
	_ = live.Close()

	out := filepath.Join(dir, "stdout.bin")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = f
	err = runBackup([]string{"-db", src}, store.Open)
	os.Stdout = saved
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, []byte("SQLite format 3\x00")) {
		t.Fatalf("stdout is not an SQLite database (first bytes: %q)", got[:min(16, len(got))])
	}
}

// TestPerChatScopeLayersOnTheSharedCorpus is the property that makes
// per_chat safe to switch on: the seeded corpus lives under "global", so a
// chat scope read on its own would be empty and every message in that chat
// unscoreable. The adapter must return the sum.
func TestPerChatScopeLayersOnTheSharedCorpus(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := train.RecordTokens(db, "global", "spam", "preset", []string{"casino", "bonus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := train.RecordTokens(db, chatScope(-100123), "spam", "user", []string{"casino"}); err != nil {
		t.Fatal(err)
	}

	a := bayesAdapter{db: db}
	spam, _, err := a.TokenCounts(chatScope(-100123), []string{"casino", "bonus"})
	if err != nil {
		t.Fatal(err)
	}
	// "casino" appears in both corpora, "bonus" only in the shared one —
	// and the chat must still see it.
	if spam["casino"] != 2 || spam["bonus"] != 1 {
		t.Fatalf("counts = %v, want casino=2 bonus=1 (chat layered on global)", spam)
	}

	totals, err := a.Totals(chatScope(-100123))
	if err != nil {
		t.Fatal(err)
	}
	if totals.SpamDocs != 2 {
		t.Fatalf("SpamDocs = %d, want 2 (one shared + one chat-local)", totals.SpamDocs)
	}

	// The shared scope must NOT double-count itself.
	totals, err = a.Totals("global")
	if err != nil {
		t.Fatal(err)
	}
	if totals.SpamDocs != 1 {
		t.Fatalf("global SpamDocs = %d, want 1", totals.SpamDocs)
	}
}
