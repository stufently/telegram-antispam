package store

import "testing"

func TestBumpBayesAccumulatesTokenCounts(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if err := db.BumpBayes("global", "spam", []string{"casino", "free"}); err != nil {
		t.Fatal(err)
	}
	if err := db.BumpBayes("global", "spam", []string{"casino", "free"}); err != nil {
		t.Fatal(err)
	}

	spam, ham, err := db.TokenCounts("global", []string{"casino", "free"})
	if err != nil {
		t.Fatal(err)
	}
	if spam["casino"] != 2 {
		t.Fatalf("spam[casino] = %d, want 2", spam["casino"])
	}
	if spam["free"] != 2 {
		t.Fatalf("spam[free] = %d, want 2", spam["free"])
	}
	if ham["casino"] != 0 {
		t.Fatalf("ham[casino] = %d, want 0", ham["casino"])
	}
	if ham["free"] != 0 {
		t.Fatalf("ham[free] = %d, want 0", ham["free"])
	}
}

func TestBumpBayesUpdatesTotals(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if err := db.BumpBayes("global", "spam", []string{"casino", "free"}); err != nil {
		t.Fatal(err)
	}
	if err := db.BumpBayes("global", "spam", []string{"casino", "free"}); err != nil {
		t.Fatal(err)
	}

	spamDocs, hamDocs, spamTok, hamTok, err := db.BayesTotals("global")
	if err != nil {
		t.Fatal(err)
	}
	if spamDocs != 2 {
		t.Fatalf("spamDocs = %d, want 2", spamDocs)
	}
	if hamDocs != 0 {
		t.Fatalf("hamDocs = %d, want 0", hamDocs)
	}
	if spamTok != 4 {
		t.Fatalf("spamTok = %d, want 4", spamTok)
	}
	if hamTok != 0 {
		t.Fatalf("hamTok = %d, want 0", hamTok)
	}
}

func TestBumpBayesCountsRepeatedTokenWithinOneCall(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if err := db.BumpBayes("global", "spam", []string{"a", "a"}); err != nil {
		t.Fatal(err)
	}

	spam, _, err := db.TokenCounts("global", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if spam["a"] != 2 {
		t.Fatalf("spam[a] = %d, want 2 (per-occurrence bump)", spam["a"])
	}

	spamDocs, _, spamTok, _, err := db.BayesTotals("global")
	if err != nil {
		t.Fatal(err)
	}
	if spamDocs != 1 {
		t.Fatalf("spamDocs = %d, want 1", spamDocs)
	}
	if spamTok != 2 {
		t.Fatalf("spamTok = %d, want 2", spamTok)
	}
}

func TestTokenCountsDefaultsToZeroForUnknownTokens(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	spam, ham, err := db.TokenCounts("global", []string{"nope"})
	if err != nil {
		t.Fatal(err)
	}
	if spam["nope"] != 0 {
		t.Fatalf("spam[nope] = %d, want 0", spam["nope"])
	}
	if ham["nope"] != 0 {
		t.Fatalf("ham[nope] = %d, want 0", ham["nope"])
	}
}

func TestBayesTotalsDefaultsToZeroForUnknownScope(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	spamDocs, hamDocs, spamTok, hamTok, err := db.BayesTotals("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if spamDocs != 0 || hamDocs != 0 || spamTok != 0 || hamTok != 0 {
		t.Fatalf("totals = (%d,%d,%d,%d), want all 0", spamDocs, hamDocs, spamTok, hamTok)
	}
}

func TestBumpBayesSeparatesHamAndSpamAndScopes(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	if err := db.BumpBayes("global", "spam", []string{"casino"}); err != nil {
		t.Fatal(err)
	}
	if err := db.BumpBayes("global", "ham", []string{"casino"}); err != nil {
		t.Fatal(err)
	}
	if err := db.BumpBayes("other-scope", "spam", []string{"casino"}); err != nil {
		t.Fatal(err)
	}

	spam, ham, err := db.TokenCounts("global", []string{"casino"})
	if err != nil {
		t.Fatal(err)
	}
	if spam["casino"] != 1 {
		t.Fatalf("spam[casino] = %d, want 1", spam["casino"])
	}
	if ham["casino"] != 1 {
		t.Fatalf("ham[casino] = %d, want 1", ham["casino"])
	}

	spamDocsOther, _, spamTokOther, _, err := db.BayesTotals("other-scope")
	if err != nil {
		t.Fatal(err)
	}
	if spamDocsOther != 1 || spamTokOther != 1 {
		t.Fatalf("other-scope totals = (%d,%d), want (1,1)", spamDocsOther, spamTokOther)
	}
}
