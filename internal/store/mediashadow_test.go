package store

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestIncidentsWithSignalMatchesTheSignalNameExactly(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	review := func(extra ...domain.Signal) domain.Verdict {
		return domain.Verdict{Action: domain.ActionQuarantine, Scope: domain.ScopeGlobal, Reason: "captionless_media",
			Signals: append([]domain.Signal{{Name: "captionless_media", Detail: "kinds=photo len=0"}}, extra...)}
	}
	labelled, _, err := db.InsertPending(-100, 10, 7, 0, false, review(domain.Signal{Name: "jev_image", Detail: "promo=0.97 contact=0.10 ordinary=0.02"}))
	if err != nil {
		t.Fatal(err)
	}
	// The name appears only in another signal's DETAIL: not a match.
	if _, _, err := db.InsertPending(-100, 11, 7, 0, false, review(domain.Signal{Name: "fake_admin", Detail: `"jev_image"`})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.InsertPending(-100, 12, 7, 0, true, review()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.RecordDecision(labelled, "enf"); err != nil {
		t.Fatal(err)
	}
	got, err := db.IncidentsWithSignal("jev_image", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != labelled || got[0].ChatID != -100 || got[0].MessageID != 10 ||
		got[0].Decision != "enf" || got[0].Detail != "promo=0.97 contact=0.10 ordinary=0.02" || got[0].DryRun {
		t.Fatalf("rows = %+v", got)
	}
	// since is inclusive of now and excludes the past.
	if got, err := db.IncidentsWithSignal("jev_image", 1<<40); err != nil || len(got) != 0 {
		t.Fatalf("future since: %+v %v", got, err)
	}
	// A LIKE wildcard in the name is literal.
	if got, err := db.IncidentsWithSignal("jev%", 0); err != nil || len(got) != 0 {
		t.Fatalf("wildcard matched: %+v %v", got, err)
	}
}
