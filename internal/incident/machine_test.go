package incident

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// stubRepo is a minimal in-memory Repo.
type stubRepo struct {
	state     domain.IncidentState
	fresh     bool
	verdict   domain.Verdict
	tokens    []string
	tokensErr error
	savedIDs  []int
	// evidenceCalls / evidenceIDs record AddEvidence. What is stored here is
	// what the admin buttons later act on, so "was it called" and "with
	// which copies" are separate questions from the incident's state.
	evidenceCalls int
	evidenceIDs   []int
}

func (r *stubRepo) InsertPending(_ int64, _ int, _, _ int64, _ bool, verdict domain.Verdict) (int64, bool, error) {
	r.verdict = verdict
	return 1, r.fresh, nil
}
func (r *stubRepo) SetIncidentState(_ int64, s domain.IncidentState) error { r.state = s; return nil }
func (r *stubRepo) AddEvidence(_ int64, _ int64, ids []int) error {
	r.evidenceCalls++
	r.evidenceIDs = ids
	return nil
}
func (r *stubRepo) SaveIncidentMessageIDs(_ int64, ids []int) error {
	r.savedIDs = ids
	return nil
}
func (r *stubRepo) SaveIncidentTokens(_ int64, tokens []string) error {
	r.tokens = tokens
	return r.tokensErr
}

func liveIncident(dry bool) domain.Incident {
	return domain.Incident{
		ChatID: -100123, MessageIDs: []int{55}, DryRun: dry,
		Sender:  domain.Sender{UserID: 7, Kind: domain.SenderUser},
		Verdict: domain.Verdict{Action: domain.ActionBan, Confidence: 0.99},
	}
}

func TestEvidenceBeforeActionAndDelete(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{fresh: true}, 999)
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
	m := New(f, &stubRepo{fresh: true}, 999)
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
	repo := &stubRepo{fresh: true}
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

// TestEvidenceFailureBlocklistStillActs pins which verdicts may be enforced
// with no evidence in the admin chat: only the externally verifiable one.
// A blocklist hit is a published CAS/LOLS ban an admin can check without the
// copied message; a Bayes or LLM call is precisely what the buttons under
// that copy exist to let a human overturn, so it must fail closed.
func TestEvidenceFailureBlocklistStillActs(t *testing.T) {
	f := fake.New()
	f.CopyErr = errors.New("copy failed")
	repo := &stubRepo{fresh: true}
	m := New(f, repo, 999)
	inc := liveIncident(false) // Action ban, not dry-run
	inc.Verdict.Signals = []domain.Signal{{Name: "blocklist"}}

	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatalf("blocklist deny should proceed despite evidence failure, got err %v", err)
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
		t.Fatalf("blocklist deny must ban and delete despite evidence failure; calls=%v", f.Calls())
	}
	if repo.state != domain.StateDone {
		t.Fatalf("final state = %v, want done", repo.state)
	}
}

// TestEvidenceFailureProbabilisticVerdictStopsRegardlessOfConfidence covers
// the regression that made the guard meaningless: every detector stamps
// Confidence 1.0, so a confidence-based gate always let the sanction
// through and produced mutes with no evidence to review.
func TestEvidenceFailureProbabilisticVerdictStopsRegardlessOfConfidence(t *testing.T) {
	f := fake.New()
	f.CopyErr = errors.New("copy failed")
	repo := &stubRepo{fresh: true}
	m := New(f, repo, 999)
	inc := liveIncident(false)
	inc.Verdict.Confidence = 1.0
	inc.Verdict.Signals = []domain.Signal{{Name: "llm"}}

	if err := m.Handle(context.Background(), inc); err == nil {
		t.Fatal("an LLM verdict with no evidence must not be enforced")
	}
	for _, c := range f.Calls() {
		if c == "BanMember" || c == "RestrictMember" || c == "DeleteMessages" {
			t.Fatalf("must not act on a probabilistic verdict without evidence; calls=%v", f.Calls())
		}
	}
	if repo.state != domain.StateEvidenceFailed {
		t.Fatalf("state = %v, want evidence_failed", repo.state)
	}
}

func TestDryRunStillCopiesEvidence(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{fresh: true}, 999)
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

func TestReprocessGuardSkipsDuplicate(t *testing.T) {
	f := fake.New()
	repo := &stubRepo{fresh: false} // incident already exists
	m := New(f, repo, 999)
	if err := m.Handle(context.Background(), liveIncident(false)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c == "CopyMessages" || c == "BanMember" {
			t.Fatalf("duplicate incident must be skipped; calls=%v", f.Calls())
		}
	}
}

func TestEvidenceFailureNotifiesAdminWhetherOrNotItActs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal string
	}{
		{"blocklist deny acts", "blocklist"},
		{"probabilistic verdict does not act", "bayes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fake.New()
			f.CopyErr = errors.New("copy failed")
			m := New(f, &stubRepo{fresh: true}, 999)
			inc := liveIncident(false)
			inc.Verdict.Signals = []domain.Signal{{Name: tc.signal}}
			_ = m.Handle(context.Background(), inc)
			var notified bool
			for _, c := range f.Calls() {
				if c == "SendAdmin" {
					notified = true
				}
			}
			// Silence is the failure mode that matters: a detection nobody
			// hears about is indistinguishable from no detection at all.
			if !notified {
				t.Fatalf("failed evidence must still notify admin; calls=%v", f.Calls())
			}
		})
	}
}

// TestSilentlyEmptyCopyIsAnEvidenceFailure covers the case Telegram reports
// as success: copyMessages skips what it cannot copy — a quiz poll, whose
// option texts the detectors do read — and returns an EMPTY id list with no
// error. Read literally that is "evidence copied", and the machine used to
// mark the incident evidenced and sanction on it, leaving the admin chat with
// a card, undo buttons and nothing underneath to judge them by.
func TestSilentlyEmptyCopyIsAnEvidenceFailure(t *testing.T) {
	f := fake.New()
	f.CopyOmit = 1 // one message asked for, none copied, no error
	repo := &stubRepo{fresh: true}
	m := New(f, repo, 999)
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{{Name: "bayes"}}

	if err := m.Handle(context.Background(), inc); err == nil {
		t.Fatal("a probabilistic verdict with no copied evidence must not be enforced")
	}
	for _, c := range f.Calls() {
		if c == "BanMember" || c == "RestrictMember" || c == "DeleteMessages" {
			t.Fatalf("must not act with no copied evidence; calls=%v", f.Calls())
		}
	}
	if repo.state != domain.StateEvidenceFailed {
		t.Fatalf("state = %v, want evidence_failed", repo.state)
	}
	// Nothing was copied, so nothing may be recorded as evidence: a stored
	// empty row would let the admin buttons act as if there were something
	// to review.
	if repo.evidenceCalls != 0 {
		t.Fatalf("AddEvidence called %d time(s) with nothing copied", repo.evidenceCalls)
	}
	var notified bool
	for _, c := range f.Calls() {
		if c == "SendAdmin" {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("an empty copy must still notify admin; calls=%v", f.Calls())
	}
}

// TestSilentlyEmptyCopyBlocklistStillActs pins that the empty copy takes the
// EXISTING no-evidence branch rather than a parallel one of its own: the same
// externally verifiable verdict that survives a failed copy survives this too.
func TestSilentlyEmptyCopyBlocklistStillActs(t *testing.T) {
	f := fake.New()
	f.CopyOmit = 1
	repo := &stubRepo{fresh: true}
	m := New(f, repo, 999)
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{{Name: "blocklist"}}

	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatalf("blocklist deny should proceed despite an empty copy, got err %v", err)
	}
	var banned bool
	for _, c := range f.Calls() {
		if c == "BanMember" {
			banned = true
		}
	}
	if !banned {
		t.Fatalf("blocklist deny must act despite an empty copy; calls=%v", f.Calls())
	}
	if repo.state != domain.StateDone {
		t.Fatalf("final state = %v, want done", repo.state)
	}
}

// TestPartialCopySanctionsAndSaysSoOnTheCard covers the album whose parts did
// not all copy. The sanction stands — what did copy is what the verdict was
// reached on — but the card must not let a reviewer mistake the copies for
// the whole message.
func TestPartialCopySanctionsAndSaysSoOnTheCard(t *testing.T) {
	f := fake.New()
	f.CopyOmit = 1 // 3 parts asked for, 2 copied, no error
	repo := &stubRepo{fresh: true}
	m := New(f, repo, 999)
	inc := liveIncident(false)
	inc.MessageIDs = []int{55, 56, 57}
	inc.Verdict.Signals = []domain.Signal{{Name: "bayes"}}

	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatalf("a partially copied incident must still be handled, got err %v", err)
	}
	var banned bool
	for _, c := range f.Calls() {
		if c == "BanMember" {
			banned = true
		}
	}
	if !banned {
		t.Fatalf("partial evidence must not cancel the sanction; calls=%v", f.Calls())
	}
	if repo.state != domain.StateDone {
		t.Fatalf("final state = %v, want done", repo.state)
	}
	if got := len(f.LastAdmin.CopyMessageIDs); got != 2 {
		t.Fatalf("card carries %d copy ids, want the 2 that were copied", got)
	}
	// The stored evidence is the copies that exist, not the ids asked for:
	// the buttons must not later reach for a copy Telegram never made.
	if repo.evidenceCalls != 1 || len(repo.evidenceIDs) != 2 {
		t.Fatalf("AddEvidence calls=%d ids=%v, want one call with the 2 real copies", repo.evidenceCalls, repo.evidenceIDs)
	}
	if !strings.Contains(f.LastAdmin.Text, "copied 2 of 3") {
		t.Fatalf("card must say the evidence is incomplete, got:\n%s", f.LastAdmin.Text)
	}
}

// TestCompleteCopyAddsNoIncompleteNote is the other half: the note must not
// appear when everything copied, or it stops meaning anything.
func TestCompleteCopyAddsNoIncompleteNote(t *testing.T) {
	f := fake.New()
	repo := &stubRepo{fresh: true}
	m := New(f, repo, 999)
	inc := liveIncident(false)
	inc.MessageIDs = []int{55, 56}

	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.LastAdmin.Text, "INCOMPLETE") {
		t.Fatalf("a fully copied incident must not be flagged incomplete, got:\n%s", f.LastAdmin.Text)
	}
	if repo.evidenceCalls != 1 || len(repo.evidenceIDs) != 2 {
		t.Fatalf("AddEvidence calls=%d ids=%v, want one call with both copies", repo.evidenceCalls, repo.evidenceIDs)
	}
	if repo.state != domain.StateDone {
		t.Fatalf("final state = %v, want done", repo.state)
	}
}

func TestEphemeralNoticeOnLiveSanction(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{fresh: true}, 999)
	m.EphemeralNotice = true
	m.EphemeralText = "removed pending review"
	if err := m.Handle(context.Background(), liveIncident(false)); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	idx := map[string]int{}
	count := map[string]int{}
	for i, c := range calls {
		count[c]++
		if _, ok := idx[c]; !ok {
			idx[c] = i
		}
	}
	if count["SendEphemeral"] != 1 {
		t.Fatalf("SendEphemeral called %d times, want 1; calls=%v", count["SendEphemeral"], calls)
	}
	if !(idx["DeleteMessages"] < idx["SendEphemeral"]) {
		t.Fatalf("ephemeral notice must follow delete; calls=%v", calls)
	}
	want := struct {
		Chat, UserID int64
		Text         string
	}{Chat: -100123, UserID: 7, Text: "removed pending review"}
	if f.LastEphemeral.Chat != want.Chat || f.LastEphemeral.UserID != want.UserID || f.LastEphemeral.Text != want.Text {
		t.Fatalf("LastEphemeral = %+v, want %+v", f.LastEphemeral, want)
	}
}

func TestEphemeralNoticeSkippedOnDryRun(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{fresh: true}, 999)
	m.EphemeralNotice = true
	m.EphemeralText = "removed pending review"
	if err := m.Handle(context.Background(), liveIncident(true)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c == "SendEphemeral" {
			t.Fatalf("dry-run must not send ephemeral notice; calls=%v", f.Calls())
		}
	}
}

func TestEphemeralNoticeSkippedWhenDisabled(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{fresh: true}, 999)
	// EphemeralNotice defaults to false.
	if err := m.Handle(context.Background(), liveIncident(false)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c == "SendEphemeral" {
			t.Fatalf("disabled ephemeral notice must not be sent; calls=%v", f.Calls())
		}
	}
}

func TestEphemeralNoticeErrorIsBestEffort(t *testing.T) {
	f := fake.New()
	f.EphemeralErr = errors.New("send failed")
	m := New(f, &stubRepo{fresh: true}, 999)
	m.EphemeralNotice = true
	m.EphemeralText = "removed pending review"
	if err := m.Handle(context.Background(), liveIncident(false)); err != nil {
		t.Fatalf("ephemeral notice error must not fail Handle, got %v", err)
	}
}

func TestButtonsAttachedToAdminMessage(t *testing.T) {
	f := fake.New()
	m := New(f, &stubRepo{fresh: true}, 999)
	m.SetButtons(func(key string, _ bool) [][]telegram.Button {
		return [][]telegram.Button{{{Text: "FP", Data: "fp:" + key}}}
	})
	if err := m.Handle(context.Background(), liveIncident(true)); err != nil { // dry-run: still sends admin
		t.Fatal(err)
	}
	if len(f.LastAdmin.Buttons) == 0 {
		t.Fatal("admin message should carry action buttons")
	}
}

// A review verdict must never be enforced automatically, and the machine is
// the last line of that defence: the wiring already turns such an incident
// into a dry-run one, but the two must not be able to disagree.
func TestReviewOnlyVerdictIsNeverEnforcedEvenInALiveChat(t *testing.T) {
	f := fake.New()
	r := &stubRepo{fresh: true}
	m := New(f, r, 999)

	inc := liveIncident(false) // live chat: dry_run is FALSE
	inc.Verdict.ReviewOnly = true
	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		switch c {
		case "DeleteMessages", "RestrictMember", "BanMember", "BanSenderChat":
			t.Fatalf("review verdict issued %s: a hint must never sanction on its own", c)
		}
	}
	if f.LastAdmin.SourceChatID == 0 {
		t.Fatal("the evidence must still reach the admin chat — that is the whole point")
	}
}

func TestEnforceReportsTheHalfThatLanded(t *testing.T) {
	inc := liveIncident(false)
	// delete_mute, so the sanction goes through RestrictMember and the test
	// can fail that half on its own.
	inc.Verdict.Action = domain.ActionDeleteMute

	// Mute lands, delete fails: the message is still in the chat and the
	// moderator has to be told so.
	f := fake.New()
	f.DeleteErr = errors.New("message to delete not found")
	out := New(f, &stubRepo{fresh: true}, 999).Enforce(context.Background(), 1, inc)
	if !out.Sanctioned || out.Deleted || out.Err == nil {
		t.Fatalf("sanctioned=%v deleted=%v err=%v, want sanction only", out.Sanctioned, out.Deleted, out.Err)
	}

	// Delete lands, mute fails.
	f = fake.New()
	f.RestrictErr = errors.New("not enough rights")
	out = New(f, &stubRepo{fresh: true}, 999).Enforce(context.Background(), 1, inc)
	if out.Sanctioned || !out.Deleted || out.Err == nil {
		t.Fatalf("sanctioned=%v deleted=%v err=%v, want delete only", out.Sanctioned, out.Deleted, out.Err)
	}

	// Both land.
	f = fake.New()
	out = New(f, &stubRepo{fresh: true}, 999).Enforce(context.Background(), 1, inc)
	if !out.Sanctioned || !out.Deleted || out.Err != nil {
		t.Fatalf("clean run reported %+v", out)
	}
}

func TestAlbumMessageIDsArePersistedForALaterSanction(t *testing.T) {
	f := fake.New()
	r := &stubRepo{fresh: true}
	inc := liveIncident(true)
	inc.MessageIDs = []int{55, 56, 57}
	if err := New(f, r, 999).Handle(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	if len(r.savedIDs) != 3 {
		t.Fatalf("saved album ids = %v, want all three so a later enforce removes the whole album", r.savedIDs)
	}
}
