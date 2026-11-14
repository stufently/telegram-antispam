package incident

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/recent"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

// deletePort records every DeleteMessages call (the fake keeps only the
// last) and can fail the calls that touch a given id.
type deletePort struct {
	*fake.Fake
	deletes [][]int
	failOn  int
}

func (p *deletePort) DeleteMessages(ctx context.Context, chat int64, ids []int) error {
	p.deletes = append(p.deletes, append([]int(nil), ids...))
	for _, id := range ids {
		if p.failOn != 0 && id == p.failOn {
			return errors.New("message can't be deleted")
		}
	}
	return p.Fake.DeleteMessages(ctx, chat, ids)
}

const (
	hChat  = int64(-1002845427973)
	hUser  = int64(77)
	hOther = int64(78)
)

var hNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// historySetup builds a machine whose buffer holds, for the sender: 2417
// (2h ago, outside the 1h window) and 2419 (30m ago); plus 2418 from
// another user and 3000 from the same user in another chat.
func historySetup(trusted bool, trustErr error) (*Machine, *deletePort, *stubRepo) {
	clock := hNow
	buf := recent.New(func() time.Duration { return 3 * time.Hour })
	buf.SetClock(func() time.Time { return clock })
	clock = hNow.Add(-2 * time.Hour)
	buf.Record(hChat, hUser, 2417, time.Time{})
	clock = hNow.Add(-30 * time.Minute)
	buf.Record(hChat, hUser, 2419, time.Time{})
	buf.Record(hChat, hOther, 2418, time.Time{})
	buf.Record(-100999, hUser, 3000, time.Time{})
	clock = hNow
	buf.Record(hChat, hUser, 2420, time.Time{}) // the incident's own message

	p := &deletePort{Fake: fake.New()}
	repo := &stubRepo{fresh: true}
	m := New(p, repo, 999)
	m.History = buf
	m.HistoryWindow = func() time.Duration { return time.Hour }
	m.Trusted = func(chat, user int64) (bool, error) {
		if chat != hChat || user != hUser {
			panic("trust looked up for the wrong sender")
		}
		return trusted, trustErr
	}
	m.Now = func() time.Time { return hNow }
	return m, p, repo
}

func historyIncident() domain.Incident {
	return domain.Incident{
		ChatID: hChat, MessageIDs: []int{2420},
		Sender:  domain.Sender{UserID: hUser, Kind: domain.SenderUser},
		Verdict: domain.Verdict{Action: domain.ActionDeleteMute, Reason: "bayes", Signals: []domain.Signal{{Name: "bayes"}}},
	}
}

func TestDeleteMuteRemovesUntrustedSendersRecentMessages(t *testing.T) {
	m, p, repo := historySetup(false, nil)
	if err := m.Handle(context.Background(), historyIncident()); err != nil {
		t.Fatal(err)
	}
	want := [][]int{{2420}, {2419}}
	if !reflect.DeepEqual(p.deletes, want) {
		t.Fatalf("deletes = %v, want %v (own message, then only the in-window earlier one of this sender in this chat)", p.deletes, want)
	}
	if !strings.Contains(p.LastAdmin.Text, "also deleting 1 earlier message(s) of this sender from the last 1h") {
		t.Fatalf("card must say how many earlier messages go, got:\n%s", p.LastAdmin.Text)
	}
	if repo.state != domain.StateDone {
		t.Fatalf("state = %v, want done", repo.state)
	}
	// The earlier messages go AFTER the evidence card and the sanction.
	calls := p.Calls()
	last := -1
	for i, c := range calls {
		if c == "SendAdmin" || c == "RestrictMember" {
			last = i
		}
	}
	if last < 0 || calls[len(calls)-1] != "DeleteMessages" || last > len(calls)-3 {
		t.Fatalf("earlier messages must be deleted last; calls=%v", calls)
	}
}

func TestDeleteMuteKeepsTrustedSendersMessages(t *testing.T) {
	m, p, _ := historySetup(true, nil)
	if err := m.Handle(context.Background(), historyIncident()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.deletes, [][]int{{2420}}) {
		t.Fatalf("deletes = %v, want only the incident's own message", p.deletes)
	}
	if strings.Contains(p.LastAdmin.Text, "earlier message") {
		t.Fatalf("card must not announce a cleanup that does not happen:\n%s", p.LastAdmin.Text)
	}
}

func TestDeleteMuteTrustLookupErrorKeepsMessages(t *testing.T) {
	m, p, _ := historySetup(false, errors.New("database is locked"))
	if err := m.Handle(context.Background(), historyIncident()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.deletes, [][]int{{2420}}) {
		t.Fatalf("deletes = %v, want only the incident's own message on a trust error", p.deletes)
	}
}

func TestEarlierMessageDeleteFailureDoesNotFailIncident(t *testing.T) {
	m, p, repo := historySetup(false, nil)
	p.failOn = 2419
	if err := m.Handle(context.Background(), historyIncident()); err != nil {
		t.Fatalf("a failed cleanup of earlier messages must not fail the incident: %v", err)
	}
	if repo.state != domain.StateDone {
		t.Fatalf("state = %v, want done", repo.state)
	}
	out := m.Enforce(context.Background(), 1, historyIncident())
	if !out.Sanctioned || !out.Deleted || out.Err != nil || out.PriorDeleted != 0 {
		t.Fatalf("outcome = %+v, want sanctioned+deleted, no error, 0 earlier deleted", out)
	}
}

func TestEnforceButtonPathAlsoRemovesEarlierMessages(t *testing.T) {
	m, p, _ := historySetup(false, nil)
	out := m.Enforce(context.Background(), 1, historyIncident())
	if out.PriorDeleted != 1 || out.Err != nil {
		t.Fatalf("outcome = %+v, want 1 earlier message deleted", out)
	}
	if !reflect.DeepEqual(p.deletes, [][]int{{2420}, {2419}}) {
		t.Fatalf("deletes = %v", p.deletes)
	}
}

func TestEarlierMessagesOnlyForDeleteMute(t *testing.T) {
	for _, a := range []domain.Action{domain.ActionBan, domain.ActionMute, domain.ActionDeleteOnly} {
		m, p, _ := historySetup(false, nil)
		inc := historyIncident()
		inc.Verdict.Action = a
		if err := m.Handle(context.Background(), inc); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p.deletes, [][]int{{2420}}) {
			t.Fatalf("action %s: deletes = %v, want only the incident's own message", a, p.deletes)
		}
	}
}

func TestEarlierMessagesNotTouchedInDryRunOrReview(t *testing.T) {
	for _, mod := range []func(*domain.Incident){
		func(i *domain.Incident) { i.DryRun = true },
		func(i *domain.Incident) { i.Verdict.ReviewOnly = true },
	} {
		m, p, _ := historySetup(false, nil)
		inc := historyIncident()
		mod(&inc)
		if err := m.Handle(context.Background(), inc); err != nil {
			t.Fatal(err)
		}
		if len(p.deletes) != 0 || strings.Contains(p.LastAdmin.Text, "earlier message") {
			t.Fatalf("dry-run/review: deletes=%v card=%q", p.deletes, p.LastAdmin.Text)
		}
	}
}

func TestEarlierMessagesDisabledByZeroWindowOrChannelSender(t *testing.T) {
	m, p, _ := historySetup(false, nil)
	m.HistoryWindow = func() time.Duration { return 0 }
	m.Trusted = func(int64, int64) (bool, error) {
		t.Fatal("trust looked up with the cleanup disabled")
		return false, nil
	}
	if err := m.Handle(context.Background(), historyIncident()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.deletes, [][]int{{2420}}) {
		t.Fatalf("window 0: deletes = %v", p.deletes)
	}

	m, p, _ = historySetup(false, nil)
	inc := historyIncident()
	inc.Sender = domain.Sender{UserID: hUser, SenderChatID: -100555, Kind: domain.SenderExternalChannel}
	if err := m.Handle(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.deletes, [][]int{{2420}}) {
		t.Fatalf("channel sender: deletes = %v", p.deletes)
	}
}

func TestEarlierMessagesWindowIsInclusiveOfItsEdge(t *testing.T) {
	m, p, _ := historySetup(false, nil)
	// 2h window now reaches 2417 as well.
	m.HistoryWindow = func() time.Duration { return 2 * time.Hour }
	if err := m.Handle(context.Background(), historyIncident()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.deletes, [][]int{{2420}, {2417, 2419}}) {
		t.Fatalf("deletes = %v, want 2417 and 2419 with a 2h window", p.deletes)
	}
	if !strings.Contains(p.LastAdmin.Text, "also deleting 2 earlier message(s) of this sender from the last 2h") {
		t.Fatalf("card:\n%s", p.LastAdmin.Text)
	}
}

func TestEnforceWithoutHistoryKeepsEarlierMessages(t *testing.T) {
	m, p, _ := historySetup(false, nil)
	out := m.EnforceWithoutHistory(context.Background(), 1, historyIncident())
	if out.PriorDeleted != 0 || !out.Deleted || !out.Sanctioned {
		t.Fatalf("outcome = %+v", out)
	}
	if !reflect.DeepEqual(p.deletes, [][]int{{2420}}) {
		t.Fatalf("deletes = %v, want only the incident's own message", p.deletes)
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Hour: "1h", 90 * time.Minute: "1h30m", 30 * time.Minute: "30m",
		30 * time.Second: "30s", 90 * time.Second: "1m30s", 2*time.Hour + 5*time.Second: "2h0m5s",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
