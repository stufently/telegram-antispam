package store

import "testing"

func TestRecordSampleAtomicInsertAndBump(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()

	tokens := []string{"casino", "free"}

	added, err := db.RecordSample("global", "spam", "import", "hash-1", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("first call: added = false, want true")
	}

	spam, _, err := db.TokenCounts("global", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if spam["casino"] != 1 || spam["free"] != 1 {
		t.Fatalf("token counts after fresh record = %+v, want casino=1 free=1", spam)
	}

	added, err = db.RecordSample("global", "spam", "import", "hash-1", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Fatal("second call with same hash: added = true, want false")
	}

	spamAfter, _, err := db.TokenCounts("global", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if spamAfter["casino"] != 1 || spamAfter["free"] != 1 {
		t.Fatalf("token counts changed on duplicate record: %+v, want unchanged casino=1 free=1", spamAfter)
	}
}

// A correction must MOVE a sample between classes. Adding the opposite
// label instead would leave the same text counted as both spam and ham,
// which does not cancel out — it dilutes both classes at once.
func TestRelabelSampleMovesCounts(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	tokens := []string{"работа", "лс", "доход"}
	hash := "h-relabel"

	if _, err := db.RecordSample("chat:-1", "spam", "user", hash, tokens); err != nil {
		t.Fatal(err)
	}

	relabeled, added, err := db.RelabelSample("chat:-1", "spam", "ham", hash, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if !relabeled || !added {
		t.Fatalf("expected a move, got relabeled=%v added=%v", relabeled, added)
	}

	spam, ham, err := db.TokenCounts("chat:-1", tokens)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range tokens {
		if spam[tok] != 0 {
			t.Errorf("token %q still counted as spam (%d)", tok, spam[tok])
		}
		if ham[tok] != 1 {
			t.Errorf("token %q ham count = %d, want 1", tok, ham[tok])
		}
	}

	// Repeating the same correction is a no-op, not a second subtraction:
	// counts must never go negative, since a negative count would read as
	// evidence for the opposite class rather than as no evidence.
	relabeled, added, err = db.RelabelSample("chat:-1", "spam", "ham", hash, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if relabeled || added {
		t.Fatalf("repeat correction must be a no-op, got relabeled=%v added=%v", relabeled, added)
	}
	spam, ham, err = db.TokenCounts("chat:-1", tokens)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range tokens {
		if spam[tok] != 0 || ham[tok] != 1 {
			t.Errorf("token %q drifted after repeat: spam=%d ham=%d", tok, spam[tok], ham[tok])
		}
	}
}

// An unseen sample has nothing to move, but the moderator's label is still
// worth recording — otherwise the first correction on a message the bot
// never learned would teach nothing.
func TestRelabelSampleRecordsUnseen(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	tokens := []string{"куплю", "usdt"}

	relabeled, added, err := db.RelabelSample("chat:-2", "spam", "ham", "h-new", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if relabeled {
		t.Error("nothing to relabel, yet it reported a move")
	}
	if !added {
		t.Fatal("a first-time correction must still be recorded")
	}
	_, ham, err := db.TokenCounts("chat:-2", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if ham["куплю"] != 1 {
		t.Errorf("ham count = %d, want 1", ham["куплю"])
	}
}
