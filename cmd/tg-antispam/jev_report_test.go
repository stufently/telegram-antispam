package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

func TestJEVReportComparesScoresWithDecisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	add := func(msg int, detail, decision string) {
		t.Helper()
		v := domain.Verdict{Action: domain.ActionQuarantine, Reason: "captionless_media",
			Signals: []domain.Signal{{Name: "captionless_media"}, {Name: "jev_image", Detail: detail}}}
		id, _, err := db.InsertPending(-100, msg, 7, 0, false, v)
		if err != nil {
			t.Fatal(err)
		}
		if decision != "" {
			if _, _, err := db.RecordDecision(id, decision); err != nil {
				t.Fatal(err)
			}
		}
	}
	add(1, "promo=0.97 contact=0.95 ordinary=0.02", "enf")     // high, spam
	add(2, "promo=0.95 contact=0.10 ordinary=0.10", "confirm") // high, spam
	add(3, "promo=0.92 contact=0.20 ordinary=0.30", "fp")      // high, ham
	add(4, "promo=0.50 contact=0.10 ordinary=0.50", "")        // mid, undecided
	add(5, "promo=0.05 contact=0.93 ordinary=0.90", "enf")     // low, spam (missed by promo, caught by contact)
	add(6, "promo=0.02 contact=0.01 ordinary=0.97", "lift")    // low, ham
	add(7, "garbage", "enf")                                   // unparsable: skipped
	if _, _, err := db.InsertPending(-100, 8, 7, 0, false, domain.Verdict{Action: domain.ActionQuarantine,
		Signals: []domain.Signal{{Name: "captionless_media"}}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	var out bytes.Buffer
	if err := runJEVReport([]string{"-db", path, "-days", "14"}, &out, store.Open); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"6 labelled photo card(s) in the last 14 day(s)",
		"agreement (promo >= 0.90, decided): 2/3 = 67%",
		"missed (promo < 0.30 but moderators said spam): 1 of 2 decided",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	for bucket, counts := range map[string]string{
		"promo >= 0.90":            "2  1  0  0",
		"0.30 <= promo < 0.90":     "0  0  1  0",
		"promo < 0.30":             "1  1  0  0",
		"promo or contact >= 0.90": "3  1  0  0",
	} {
		if !containsRow(got, bucket, counts) {
			t.Fatalf("bucket %q want %q in:\n%s", bucket, counts, got)
		}
	}
	if strings.Contains(got, "garbage") {
		t.Fatal("unparsable detail printed")
	}
}

func containsRow(out, bucket, counts string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, bucket+" ") && strings.Join(strings.Fields(strings.TrimPrefix(line, bucket)), "  ") == counts {
			return true
		}
	}
	return false
}

func TestJEVReportRejectsBadArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	for _, args := range [][]string{
		{"-db", path},
		{"-db", "", "-days", "1"},
		{"-db", path, "-days", "0"},
		{"-db", path, "-threshold", "1.5"},
	} {
		if err := runJEVReport(args, &bytes.Buffer{}, store.Open); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
	if !knownSubcommands["jev-report"] {
		t.Fatal("jev-report would fall through to a second bot instance")
	}
}

func TestDecisionClass(t *testing.T) {
	for in, want := range map[string]string{"confirm": "spam", "enf": "spam", "fp": "ham", "lift": "ham", "": "undecided", "delevi": "other"} {
		if got := decisionClass(in); got != want {
			t.Fatalf("%q -> %q, want %q", in, got, fmt.Sprint(want))
		}
	}
}
