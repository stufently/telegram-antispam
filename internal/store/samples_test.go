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
