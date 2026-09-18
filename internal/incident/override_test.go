package incident

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// These tests run the machine against the real SQLite store: the property
// under test — "a manual /spam acts on an incident that never sanctioned, and
// never on one that did" — lives in the conditional UPDATE, not in the
// machine, so a stub would only test the stub.

const (
	ovChat = int64(-100123)
	ovMsg  = 55
	ovUser = int64(7)
)

func newOverrideRig(t *testing.T) (*store.DB, *fake.Fake, *Machine) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	return db, f, New(f, db, 999)
}

// autoIncident is what the detector raises: a probabilistic verdict.
func autoIncident(dry bool) domain.Incident {
	return domain.Incident{
		ChatID: ovChat, MessageIDs: []int{ovMsg}, DryRun: dry,
		Sender: domain.Sender{UserID: ovUser, Kind: domain.SenderUser},
		Verdict: domain.Verdict{
			Action: domain.ActionDeleteMute, Scope: domain.ScopeGlobal, Reason: "bayes",
			Signals: []domain.Signal{{Name: "bayes"}},
		},
		Tokens: []string{"work", "dm"},
	}
}

// manualIncident is what admin.Commands builds for /spam or /ban.
func manualIncident(signal string, action domain.Action) domain.Incident {
	return domain.Incident{
		ChatID: ovChat, MessageIDs: []int{ovMsg},
		Sender: domain.Sender{UserID: ovUser, Kind: domain.SenderUser},
		Verdict: domain.Verdict{
			Action: action, Scope: domain.ScopeGlobal, Reason: signal,
			Signals: []domain.Signal{{Name: signal, Detail: "by=50 cmd_msg=10"}},
		},
	}
}

func count(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
}

type auditRow struct{ action, reason, signals, decision string }

func readAudit(t *testing.T, db *store.DB, id int64) auditRow {
	t.Helper()
	var r auditRow
	if err := db.Read().QueryRow(`
SELECT a.action, a.reason, a.signals, i.decision FROM audit a JOIN incidents i ON i.id=a.incident_id
WHERE a.incident_id=?`, id).Scan(&r.action, &r.reason, &r.signals, &r.decision); err != nil {
		t.Fatal(err)
	}
	return r
}

func onlyIncidentID(t *testing.T, db *store.DB) int64 {
	t.Helper()
	var n int
	var id int64
	if err := db.Read().QueryRow("SELECT COUNT(*), MAX(id) FROM incidents").Scan(&n, &id); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("incidents = %d, want exactly 1 (the override must reuse the row)", n)
	}
	return id
}

// The production case from the bug report: the detector's verdict fails
// closed because the evidence copy came back empty (a quiz poll), and the
// moderator then types /spam. The sanction must be applied, and the row
// must say so — or the undo buttons cannot lift it and the digest miscounts.
func TestManualSpamOverridesEvidenceFailedIncident(t *testing.T) {
	db, f, m := newOverrideRig(t)
	f.CopyOmit = 1
	_ = m.Handle(context.Background(), autoIncident(false)) // fails closed, returns an error by design
	if n := count(f.Calls(), "RestrictMember"); n != 0 {
		t.Fatalf("precondition: automatic verdict must not act without evidence, restricts=%d", n)
	}
	id := onlyIncidentID(t, db)
	if st, _ := db.GetIncidentState(id); st != domain.StateEvidenceFailed {
		t.Fatalf("precondition: state = %v, want evidence_failed", st)
	}

	fresh, err := m.HandleReport(context.Background(), manualIncident("manual_spam", domain.ActionDeleteMute))
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if !fresh {
		t.Fatal("an applied override must be reported as acted, not as already handled")
	}
	if n := count(f.Calls(), "RestrictMember"); n != 1 {
		t.Fatalf("restricts = %d, want 1; calls=%v", n, f.Calls())
	}
	if f.LastDelete.Chat != ovChat || len(f.LastDelete.IDs) != 1 || f.LastDelete.IDs[0] != ovMsg {
		t.Fatalf("original must be deleted, got %+v", f.LastDelete)
	}

	id = onlyIncidentID(t, db)
	row, err := db.GetIncident(id)
	if err != nil {
		t.Fatal(err)
	}
	if row.DryRun || row.State != domain.StateDone || row.Action != domain.ActionDeleteMute {
		t.Fatalf("row = %+v, want dry_run=0 state=done action=delete_mute", row)
	}
	if !row.Sanctioned() {
		t.Fatal("the applied sanction must be liftable from the admin chat")
	}
	a := readAudit(t, db, id)
	if a.reason != "manual_spam" || !strings.Contains(a.signals, "manual_spam") || strings.Contains(a.signals, "bayes") {
		t.Fatalf("audit must carry the moderator's verdict, got %+v", a)
	}
	if a.decision != "" {
		t.Fatalf("claim must be released after a successful override, decision=%q", a.decision)
	}
	if !strings.Contains(f.LastAdmin.Text, "manual override") {
		t.Fatalf("admin chat must be told the incident was acted on, got %q", f.LastAdmin.Text)
	}
}

// An incident that did sanction must not be sanctioned a second time, by
// either command: the user may since have been unmuted by an admin.
func TestManualOverrideRefusesEnforcedIncident(t *testing.T) {
	db, f, m := newOverrideRig(t)
	if err := m.Handle(context.Background(), autoIncident(false)); err != nil {
		t.Fatal(err)
	}
	if n := count(f.Calls(), "RestrictMember"); n != 1 {
		t.Fatalf("precondition: automatic sanction, restricts=%d", n)
	}

	for _, tc := range []struct {
		signal string
		action domain.Action
	}{{"manual_spam", domain.ActionDeleteMute}, {"manual_ban", domain.ActionBan}} {
		fresh, err := m.HandleReport(context.Background(), manualIncident(tc.signal, tc.action))
		if err != nil || fresh {
			t.Fatalf("%s on an enforced incident: fresh=%v err=%v, want already handled", tc.signal, fresh, err)
		}
	}
	if r, b := count(f.Calls(), "RestrictMember"), count(f.Calls(), "BanMember"); r != 1 || b != 0 {
		t.Fatalf("second sanction applied: restricts=%d bans=%d; calls=%v", r, b, f.Calls())
	}
	if a := readAudit(t, db, onlyIncidentID(t, db)); a.reason != "bayes" || a.decision != "" {
		t.Fatalf("refused override must leave the row alone, got %+v", a)
	}
}

// A dry-run incident (the chat was observing) was never acted on; /ban acts.
// A second /ban afterwards is the already-enforced case again.
func TestManualBanOverridesDryRunIncident(t *testing.T) {
	db, f, m := newOverrideRig(t)
	if err := m.Handle(context.Background(), autoIncident(true)); err != nil {
		t.Fatal(err)
	}
	fresh, err := m.HandleReport(context.Background(), manualIncident("manual_ban", domain.ActionBan))
	if err != nil || !fresh {
		t.Fatalf("override of a dry-run incident: fresh=%v err=%v", fresh, err)
	}
	if n := count(f.Calls(), "BanMember"); n != 1 {
		t.Fatalf("bans = %d, want 1; calls=%v", n, f.Calls())
	}
	row, _ := db.GetIncident(onlyIncidentID(t, db))
	if row.DryRun || row.Action != domain.ActionBan {
		t.Fatalf("row = %+v, want a live ban", row)
	}

	fresh, err = m.HandleReport(context.Background(), manualIncident("manual_ban", domain.ActionBan))
	if err != nil || fresh {
		t.Fatalf("repeat /ban: fresh=%v err=%v, want already handled", fresh, err)
	}
	if n := count(f.Calls(), "BanMember"); n != 1 {
		t.Fatalf("repeat /ban sanctioned again, bans=%d", n)
	}
}

// A review-only verdict is recorded as a dry-run by the wiring and, should the
// wiring ever let one through live, as a finished incident whose stored action
// is a no-op. Either way nothing was done to the sender, and /spam must act.
func TestManualSpamOverridesReviewOnlyIncident(t *testing.T) {
	for _, dry := range []bool{true, false} {
		db, f, m := newOverrideRig(t)
		inc := autoIncident(dry)
		inc.Verdict.Action = domain.ActionQuarantine
		inc.Verdict.ReviewOnly = true
		inc.Verdict.Signals = []domain.Signal{{Name: "captionless_media"}}
		if err := m.Handle(context.Background(), inc); err != nil {
			t.Fatal(err)
		}
		fresh, err := m.HandleReport(context.Background(), manualIncident("manual_spam", domain.ActionDeleteMute))
		if err != nil || !fresh {
			t.Fatalf("dry=%v: override of a review-only incident: fresh=%v err=%v", dry, fresh, err)
		}
		if n := count(f.Calls(), "RestrictMember"); n != 1 {
			t.Fatalf("dry=%v: restricts = %d, want 1", dry, n)
		}
		if row, _ := db.GetIncident(onlyIncidentID(t, db)); !row.Sanctioned() {
			t.Fatalf("dry=%v: row = %+v, want a liftable live sanction", dry, row)
		}
	}
}

// A moderator decision already in flight (the admin chat's enforce button)
// holds the claim; /spam must not race it into a double sanction.
func TestManualOverrideRespectsAClaimedDecision(t *testing.T) {
	db, f, m := newOverrideRig(t)
	if err := m.Handle(context.Background(), autoIncident(true)); err != nil {
		t.Fatal(err)
	}
	id := onlyIncidentID(t, db)
	if claimed, _, err := db.RecordDecision(id, store.ManualOverrideClaim); err != nil || !claimed {
		t.Fatalf("precondition: claim=%v err=%v", claimed, err)
	}
	fresh, err := m.HandleReport(context.Background(), manualIncident("manual_spam", domain.ActionDeleteMute))
	if err != nil || fresh {
		t.Fatalf("override under a held claim: fresh=%v err=%v, want refused", fresh, err)
	}
	if n := count(f.Calls(), "RestrictMember"); n != 0 {
		t.Fatalf("sanctioned under someone else's claim, restricts=%d", n)
	}
}

// When nothing lands, the claim goes back and the row keeps saying "never
// sanctioned", so the moderator can simply retry.
func TestFailedManualOverrideCanBeRetried(t *testing.T) {
	db, f, m := newOverrideRig(t)
	if err := m.Handle(context.Background(), autoIncident(true)); err != nil {
		t.Fatal(err)
	}
	f.RestrictErr = errors.New("429")
	f.DeleteErr = errors.New("429")
	if _, err := m.HandleReport(context.Background(), manualIncident("manual_spam", domain.ActionDeleteMute)); err == nil {
		t.Fatal("a failed override must report its error")
	}
	id := onlyIncidentID(t, db)
	if a := readAudit(t, db, id); a.decision != "" || a.reason != "bayes" {
		t.Fatalf("failed override must release the claim and leave the audit alone, got %+v", a)
	}
	if row, _ := db.GetIncident(id); !row.DryRun {
		t.Fatalf("failed override must not mark the incident live, row=%+v", row)
	}

	f.RestrictErr, f.DeleteErr = nil, nil
	fresh, err := m.HandleReport(context.Background(), manualIncident("manual_spam", domain.ActionDeleteMute))
	if err != nil || !fresh {
		t.Fatalf("retry: fresh=%v err=%v", fresh, err)
	}
	if row, _ := db.GetIncident(id); !row.Sanctioned() {
		t.Fatalf("retry must leave a live, liftable sanction, row=%+v", row)
	}
}

// The command replies to ONE part of an album; the override must remove every
// part the incident recorded, not just that one.
func TestManualOverrideDeletesTheWholeAlbum(t *testing.T) {
	_, f, m := newOverrideRig(t)
	inc := autoIncident(true)
	inc.MessageIDs = []int{ovMsg, ovMsg + 1}
	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	if _, err := m.HandleReport(context.Background(), manualIncident("manual_spam", domain.ActionDeleteMute)); err != nil {
		t.Fatal(err)
	}
	if got := f.LastDelete.IDs; len(got) != 2 || got[0] != ovMsg || got[1] != ovMsg+1 {
		t.Fatalf("deleted %v, want both album parts", got)
	}
}
