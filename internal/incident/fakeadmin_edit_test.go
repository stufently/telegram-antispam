package incident

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/detect"
	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

type editAdmins struct{}

func (editAdmins) AdminIdentities(int64) ([]detect.AdminIdentity, error) {
	return []detect.AdminIdentity{{UserID: 500, DisplayName: "Дмитрии"}}, nil
}

// Telegram gives each fresh copy a new destination ID. Keep fake port logging
// but distinguish old benign evidence from the edited message's evidence.
type editEvidencePort struct {
	*fake.Fake
	copies int
}

func (p *editEvidencePort) CopyMessages(ctx context.Context, to, from int64, ids []int) ([]int, error) {
	copied, err := p.Fake.CopyMessages(ctx, to, from, ids)
	p.copies++
	for i := range copied {
		copied[i] += 1000 * p.copies
	}
	return copied, err
}

func newEditRig(t *testing.T) (*store.DB, *editEvidencePort, *Machine, domain.Incident, domain.Incident) {
	t.Helper()
	db, f, _ := newOverrideRig(t)
	port := &editEvidencePort{Fake: f}
	m := New(port, db, 999)
	c := detect.Cascade{Trust: db, TrustThreshold: 5, Admins: editAdmins{}, FakeAdmin: detect.FakeAdminCfg{Enabled: true, MaxDistance: 1, MinFuzzyLen: 5}, Rules: detect.Rules{BlockLinksForUntrusted: true}, DefaultAction: domain.ActionDeleteMute, DefaultScope: domain.ScopeGlobal}
	msg := domain.Message{ChatID: ovChat, MessageID: ovMsg, Sender: domain.Sender{Kind: domain.SenderUser, UserID: ovUser, DisplayName: "Дмитрий"}, Text: "обычный текст"}
	if v, ok := c.Decide(msg, false); ok {
		t.Fatalf("benign name must not sanction: %+v", v)
	}
	review, ok := c.ReviewCandidate(msg)
	if !ok || !review.ReviewOnly || review.Reason != "fake_admin" {
		t.Fatalf("want name review: %+v", review)
	}
	original := domain.Incident{ChatID: msg.ChatID, MessageIDs: []int{msg.MessageID}, Sender: msg.Sender, Verdict: review, DryRun: true, Tokens: detect.Tokenize(detect.Normalize(msg))}
	msg.Text = "https://spam.example/join"
	spam, ok := c.Decide(msg, true)
	if !ok || spam.ReviewOnly || spam.Reason != "link_from_untrusted" {
		t.Fatalf("edit must trigger hard rule: %+v", spam)
	}
	edited := original
	edited.DryRun, edited.Verdict, edited.Tokens = false, spam, detect.Tokenize(detect.Normalize(msg))
	if err := m.Handle(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if count(f.Calls(), "RestrictMember") != 0 || count(f.Calls(), "DeleteMessages") != 0 {
		t.Fatal("review performed destructive calls")
	}
	return db, port, m, original, edited
}

func requireEditPromotion(t *testing.T, m *Machine, p *editEvidencePort, inc domain.Incident) {
	t.Helper()
	before := count(p.Calls(), "RestrictMember")
	fresh, err := m.HandleReport(context.Background(), inc)
	if err != nil || !fresh || count(p.Calls(), "RestrictMember") != before+1 {
		t.Fatalf("spam edit must promote review and sanction: fresh=%v err=%v calls=%v", fresh, err, p.Calls())
	}
}

func TestFakeAdminReviewSpamEditEnforces(t *testing.T) {
	db, p, m, _, edited := newEditRig(t)
	oldEvidence := append([]int(nil), p.LastAdmin.CopyMessageIDs...)
	before := len(p.Calls())
	requireEditPromotion(t, m, p, edited)
	calls := p.Calls()[before:]
	want := []string{"CopyMessages", "ChatTitle", "SendAdmin", "RestrictMember", "DeleteMessages"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("fresh evidence and card must precede sanction: %v", calls)
	}
	if reflect.DeepEqual(p.LastAdmin.CopyMessageIDs, oldEvidence) || !strings.Contains(p.LastAdmin.Text, "link_from_untrusted → applying delete_mute") || strings.Contains(p.LastAdmin.Text, "review only") {
		t.Fatalf("new verdict must reply to new evidence: %+v", p.LastAdmin)
	}
	if p.LastDelete.Chat != ovChat || !reflect.DeepEqual(p.LastDelete.IDs, []int{ovMsg}) {
		t.Fatalf("wrong deletion: %+v", p.LastDelete)
	}
	id := onlyIncidentID(t, db)
	row, err := db.GetIncident(id)
	if err != nil || row.DryRun || !row.Sanctioned() || row.State != domain.StateDone {
		t.Fatalf("sanction must be stored and undoable: %+v %v", row, err)
	}
	audit := readAudit(t, db, id)
	if audit.reason != "link_from_untrusted" || audit.action != "delete_mute" || audit.decision != "" || strings.Contains(audit.signals, "fake_admin") {
		t.Fatalf("stale audit: %+v", audit)
	}
	tokens, ok, err := db.GetIncidentTokens(id)
	if err != nil || !ok || !reflect.DeepEqual(tokens, edited.Tokens) {
		t.Fatalf("training must use edited tokens: %v %v", tokens, err)
	}
}

func TestFakeAdminRepeatedReviewLeavesEditEligible(t *testing.T) {
	db, p, m, original, edited := newEditRig(t)
	before := p.Calls()
	fresh, err := m.HandleReport(context.Background(), original)
	if err != nil || fresh || !reflect.DeepEqual(p.Calls(), before) {
		t.Fatalf("repeat review had side effects: fresh=%v err=%v calls=%v", fresh, err, p.Calls())
	}
	if audit := readAudit(t, db, onlyIncidentID(t, db)); audit.reason != "fake_admin" || audit.decision != "" {
		t.Fatalf("repeat changed review: %+v", audit)
	}
	// A skipped repeat must not consume the claim needed by the later edit.
	requireEditPromotion(t, m, p, edited)
}

func TestFakeAdminPromotedIncidentIgnoresAutomaticRepeats(t *testing.T) {
	db, p, m, _, edited := newEditRig(t)
	requireEditPromotion(t, m, p, edited)
	before := p.Calls()
	if fresh, err := m.HandleReport(context.Background(), edited); err != nil || fresh {
		t.Fatalf("same verdict repeated: fresh=%v err=%v", fresh, err)
	}
	edited.Verdict = domain.Verdict{Action: domain.ActionBan, Reason: "bayes", Signals: []domain.Signal{{Name: "bayes"}}}
	if fresh, err := m.HandleReport(context.Background(), edited); err != nil || fresh {
		t.Fatalf("new automatic verdict repeated sanction: fresh=%v err=%v", fresh, err)
	}
	if !reflect.DeepEqual(p.Calls(), before) {
		t.Fatalf("already sanctioned incident caused more calls: %v", p.Calls())
	}
	if audit := readAudit(t, db, onlyIncidentID(t, db)); audit.reason != "link_from_untrusted" || audit.decision != "" {
		t.Fatalf("repeat changed audit: %+v", audit)
	}
}

func TestFakeAdminEditPromotionSafety(t *testing.T) {
	for _, kind := range []string{"dry run", "review verdict", "no action", "moderator decision", "non-review incident", "copy failed"} {
		t.Run(kind, func(t *testing.T) {
			db, p, m, _, edited := newEditRig(t)
			id := onlyIncidentID(t, db)
			switch kind {
			case "dry run":
				edited.DryRun = true
			case "review verdict":
				edited.Verdict.ReviewOnly = true
			case "no action":
				edited.Verdict.Action = domain.ActionNone
			case "moderator decision":
				if claimed, _, err := db.RecordDecision(id, "fp"); err != nil || !claimed {
					t.Fatalf("claim: %v %v", claimed, err)
				}
			case "non-review incident":
				if err := db.MarkIncidentEnforced(id, domain.ActionDeleteMute); err != nil {
					t.Fatal(err)
				}
				if err := db.SetIncidentState(id, domain.StateEvidenceFailed); err != nil {
					t.Fatal(err)
				}
			case "copy failed":
				p.CopyErr = errors.New("no access")
			}
			before := p.Calls()
			_, err := m.HandleReport(context.Background(), edited)
			if kind == "copy failed" {
				if err == nil {
					t.Fatal("failed fresh evidence must fail closed")
				}
				if count(p.Calls(), "RestrictMember") != 0 || count(p.Calls(), "DeleteMessages") != 0 {
					t.Fatal("old evidence must not authorize automatic sanction")
				}
				if audit := readAudit(t, db, id); audit.decision != "" {
					t.Fatalf("failed attempt kept claim: %+v", audit)
				}
				p.CopyErr = nil
				requireEditPromotion(t, m, p, edited)
			} else if err != nil || !reflect.DeepEqual(p.Calls(), before) {
				t.Fatalf("guard failed: err=%v calls=%v", err, p.Calls())
			}
		})
	}
}

func TestReviewEditPromotesAllReviewKinds(t *testing.T) {
	for _, kind := range []string{"fake_admin", "captionless_media", "bot_keyboard"} {
		for _, dry := range []bool{true, false} {
			t.Run(kind+map[bool]string{true: "/dry", false: "/live"}[dry], func(t *testing.T) {
				db, f, _ := newOverrideRig(t)
				p := &editEvidencePort{Fake: f}
				m := New(p, db, 999)
				inc := autoIncident(dry)
				inc.MessageIDs = []int{ovMsg, ovMsg + 1}
				inc.Verdict = domain.Verdict{Action: domain.ActionQuarantine, ReviewOnly: true, Reason: kind, Signals: []domain.Signal{{Name: kind}}}
				if err := m.Handle(context.Background(), inc); err != nil {
					t.Fatal(err)
				}
				// An edited album part still promotes and deletes the whole album.
				requireEditPromotion(t, m, p, autoIncident(false))
				if !reflect.DeepEqual(p.LastDelete.IDs, []int{ovMsg, ovMsg + 1}) {
					t.Fatalf("album only partly deleted: %v", p.LastDelete.IDs)
				}
			})
		}
	}
}

type editReadFailure struct{ *store.DB }

func (editReadFailure) GetIncident(int64) (store.IncidentRow, error) {
	return store.IncidentRow{}, errors.New("incident read unavailable")
}

func TestReviewEditReadFailureReleasesClaim(t *testing.T) {
	db, p, m, _, edited := newEditRig(t)
	broken := New(p, editReadFailure{db}, 999)
	before := p.Calls()
	if err := broken.Handle(context.Background(), edited); err == nil || !strings.Contains(err.Error(), "incident read unavailable") {
		t.Fatalf("unknown old verdict must fail closed: %v", err)
	}
	if !reflect.DeepEqual(p.Calls(), before) {
		t.Fatalf("acted on unreadable incident: %v", p.Calls())
	}
	if audit := readAudit(t, db, onlyIncidentID(t, db)); audit.decision != "" {
		t.Fatalf("read failure kept claim: %+v", audit)
	}
	requireEditPromotion(t, m, p, edited)
}

// The legacy stub never claims an override; real-store tests above exercise
// reading the prior verdict while the claim is held.
func (*stubRepo) GetIncident(int64) (store.IncidentRow, error) {
	return store.IncidentRow{}, errors.New("stub has no stored incident")
}
