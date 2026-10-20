package train

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

func newMigrated(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestImportSampleIsIdempotent(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	const text = "WIN FREE CASINO cash now, click http://spam.example/win"

	added, err := ImportSample(db, "global", "spam", "import", text)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("first import: added = false, want true")
	}

	n := detect.Normalize(domain.Message{Text: text})
	tokens := detect.Tokenize(n)
	spamBefore, _, err := db.TokenCounts("global", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(spamBefore) == 0 {
		t.Fatal("expected token counts to be bumped after first import")
	}

	added, err = ImportSample(db, "global", "spam", "import", text)
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Fatal("second import: added = true, want false (duplicate)")
	}

	spamAfter, _, err := db.TokenCounts("global", tokens)
	if err != nil {
		t.Fatal(err)
	}
	for tok, before := range spamBefore {
		if spamAfter[tok] != before {
			t.Fatalf("token %q count changed on duplicate import: before=%d after=%d", tok, before, spamAfter[tok])
		}
	}
}

func TestImportFileCountsAddedAndSkipped(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	path := filepath.Join(t.TempDir(), "samples.txt")
	content := "win free cash now\nclick here for prize\nlowest price viagra\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	added, skipped, err := ImportFile(db, "global", "spam", "import", path)
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}

	added, skipped, err = ImportFile(db, "global", "spam", "import", path)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("re-import added = %d, want 0", added)
	}
	if skipped != 3 {
		t.Fatalf("re-import skipped = %d, want 3", skipped)
	}
}

func TestSampleHashIsDeobfuscatedAndDeterministic(t *testing.T) {
	h1 := SampleHash("free casino")
	h2 := SampleHash("free casino")
	if h1 != h2 {
		t.Fatalf("SampleHash not deterministic: %q != %q", h1, h2)
	}
	if h1 == SampleHash("something else") {
		t.Fatal("different text produced the same hash")
	}
}

func TestRelabelTokensPreservesOrigin(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	tokens := []string{"example"}
	if _, err := RecordTokens(db, "global", "ham", "import", tokens); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RelabelTokens(db, "global", "ham", "spam", "cli", tokens); err != nil {
		t.Fatal(err)
	}
	var origin string
	if err := db.Read().QueryRow("SELECT origin FROM samples WHERE label='spam'").Scan(&origin); err != nil {
		t.Fatal(err)
	}
	if origin != "cli" {
		t.Fatalf("origin=%q, want cli", origin)
	}
}
