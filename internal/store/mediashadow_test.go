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

func TestManualOverrideKeepsTheShadowObservation(t *testing.T) {
	db := newMigrated(t)
	defer db.Close()
	id, _, err := db.InsertPending(-100, 20, 7, 0, true, domain.Verdict{Action: domain.ActionQuarantine, Reason: "captionless_media",
		Signals: []domain.Signal{{Name: "captionless_media"}, {Name: "jev_image", Detail: "promo=0.90 contact=0.10 ordinary=0.10"}}})
	if err != nil {
		t.Fatal(err)
	}
	manual := domain.Verdict{Action: domain.ActionDeleteMute, Reason: "manual_spam", Signals: []domain.Signal{{Name: "manual_spam"}}}
	if err := db.FinishManualOverride(id, manual, true); err != nil {
		t.Fatal(err)
	}
	var raw, reason string
	if err := db.Read().QueryRow("SELECT signals, reason FROM audit WHERE incident_id=?", id).Scan(&raw, &reason); err != nil {
		t.Fatal(err)
	}
	want := `[{"Name":"manual_spam","Detail":""},{"Name":"jev_image","Detail":"promo=0.90 contact=0.10 ordinary=0.10"}]`
	if raw != want || reason != "manual_spam" {
		t.Fatalf("audit = %s reason %s", raw, reason)
	}
	// Not sanctioned: the audit row is left alone entirely.
	id2, _, _ := db.InsertPending(-100, 21, 7, 0, true, domain.Verdict{Reason: "captionless_media", Signals: []domain.Signal{{Name: "captionless_media"}}})
	if err := db.FinishManualOverride(id2, manual, false); err != nil {
		t.Fatal(err)
	}
	if err := db.Read().QueryRow("SELECT signals FROM audit WHERE incident_id=?", id2).Scan(&raw); err != nil || raw != `[{"Name":"captionless_media","Detail":""}]` {
		t.Fatalf("unsanctioned audit = %s %v", raw, err)
	}
}
