package incident

import (
	"context"
	"errors"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// stubRepo is a minimal in-memory Repo.
type stubRepo struct{ state domain.IncidentState }

func (r *stubRepo) InsertPending(int64, int, int64, bool) (int64, bool, error) { return 1, true, nil }
func (r *stubRepo) SetIncidentState(_ int64, s domain.IncidentState) error     { r.state = s; return nil }
func (r *stubRepo) AddEvidence(int64, int64, []int) error                      { return nil }

func liveIncident(dry bool) domain.Incident {
	return domain.Incident{
		ChatID: -100123, MessageIDs: []int{55}, DryRun: dry,
		Sender:  domain.Sender{UserID: 7, Kind: domain.SenderUser},
		Verdict: domain.Verdict{Action: domain.ActionBan, Confidence: 0.99},
	}
}

func TestEvidenceBeforeActionAndDelete(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{}, 999)
	if err := m.Handle(context.Background(), liveIncident(false)); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	// evidence copy + admin summary must precede the ban and the delete.
	idx := map[string]int{}
	for i, c := range calls {
		if _, ok := idx[c]; !ok {
			idx[c] = i
		}
	}
	if !(idx["CopyMessages"] < idx["BanMember"] && idx["SendAdmin"] < idx["BanMember"]) {
		t.Fatalf("evidence must precede ban; calls=%v", calls)
	}
	if !(idx["BanMember"] < idx["DeleteMessages"]) {
		t.Fatalf("ban must precede delete; calls=%v", calls)
	}
}

func TestDryRunSkipsDestructiveCalls(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{}, 999)
	if err := m.Handle(context.Background(), liveIncident(true)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c == "BanMember" || c == "DeleteMessages" || c == "RestrictMember" {
			t.Fatalf("dry-run performed destructive call %q; calls=%v", c, f.Calls())
		}
	}
}

func TestEvidenceFailureStopsBeforeAction(t *testing.T) {
	f := fake.New()
	f.CopyErr = errors.New("copy failed")
	repo := &stubRepo{}
	m := New(f, repo, 999)
	inc := liveIncident(false)
	inc.Verdict.Confidence = 0.4 // low confidence
	_ = m.Handle(context.Background(), inc)
	for _, c := range f.Calls() {
		if c == "BanMember" || c == "DeleteMessages" {
			t.Fatalf("must not act after evidence failure on low confidence; calls=%v", f.Calls())
		}
	}
	if repo.state != domain.StateEvidenceFailed {
		t.Fatalf("state = %v, want evidence_failed", repo.state)
	}
}

func TestEvidenceFailureHighConfidenceStillActs(t *testing.T) {
	f := fake.New()
	f.CopyErr = errors.New("copy failed")
	repo := &stubRepo{}
	m := New(f, repo, 999)
	inc := liveIncident(false) // Action ban, not dry-run
	inc.Verdict.Confidence = 0.99 // hard deny, >= hardConfidence (0.9)

	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatalf("hard deny should proceed despite evidence failure, got err %v", err)
	}
	var banned, deleted bool
	for _, c := range f.Calls() {
		if c == "BanMember" {
			banned = true
		}
		if c == "DeleteMessages" {
			deleted = true
		}
	}
	if !banned || !deleted {
		t.Fatalf("hard deny must ban and delete despite evidence failure; calls=%v", f.Calls())
	}
	if repo.state != domain.StateDone {
		t.Fatalf("final state = %v, want done", repo.state)
	}
}

func TestDryRunStillCopiesEvidence(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{}, 999)
	if err := m.Handle(context.Background(), liveIncident(true)); err != nil {
		t.Fatal(err)
	}
	var copied, notified bool
	for _, c := range f.Calls() {
		if c == "CopyMessages" {
			copied = true
		}
		if c == "SendAdmin" {
			notified = true
		}
	}
	if !copied || !notified {
		t.Fatalf("dry-run must still copy evidence and notify admin; calls=%v", f.Calls())
	}
}
