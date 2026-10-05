package incident

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/admin"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

type cardPort struct {
	*fake.Fake
	reply        string
	duringDelete func()
}

func (p *cardPort) AnswerCallback(_ context.Context, _, text string) error {
	p.reply = text
	return nil
}
func (p *cardPort) DeleteMessages(ctx context.Context, chat int64, ids []int) error {
	if p.duringDelete != nil {
		f := p.duringDelete
		p.duringDelete = nil
		f()
	}
	return p.Fake.DeleteMessages(ctx, chat, ids)
}

func TestPromotedReviewRejectsOldCard(t *testing.T) {
	db, p, m, _, edited := newEditRig(t)
	m.SetButtons(admin.Buttons)
	p.SendAdminID = 20
	requireEditPromotion(t, m, p, edited)
	id := onlyIncidentID(t, db)
	port := &cardPort{Fake: p.Fake}
	h := admin.NewHandler(port, db, map[int64]bool{7: true})
	trained := 0
	h.SetTrainer(func(int64, string, []string) error { trained++; return nil })
	for _, act := range []string{"fp", "lift", "confirm", "enf", "delevi"} {
		before := len(p.Calls())
		cb := admin.Callback{Data: fmt.Sprintf("%s:%d", act, id), PresserID: 7, AdminChatID: 999, MessageID: 0}
		if err := h.Handle(context.Background(), cb); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(port.reply, "устарело") || trained != 0 {
			t.Fatalf("stale card accepted: %q trained=%d", port.reply, trained)
		}
		for _, call := range p.Calls()[before:] {
			if call != "EditAdminMarkup" {
				t.Fatalf("stale callback caused %s", call)
			}
		}
	}
	cb := admin.Callback{Data: p.LastAdmin.Buttons[0][0].Data, PresserID: 7, AdminChatID: 999, MessageID: 20}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if trained != 1 || count(p.Calls(), "UnrestrictMember") != 1 {
		t.Fatalf("new card unusable: trained=%d calls=%v", trained, p.Calls())
	}
}

func TestDeleteEvidenceKeepsConcurrentPromotion(t *testing.T) {
	db, p, m, _, edited := newEditRig(t)
	id := onlyIncidentID(t, db)
	port := &cardPort{Fake: p.Fake, duringDelete: func() {
		p.SendAdminID = 20
		requireEditPromotion(t, m, p, edited)
	}}
	h := admin.NewHandler(port, db, map[int64]bool{7: true})
	cb := admin.Callback{Data: fmt.Sprintf("delevi:%d", id), PresserID: 7, AdminChatID: 999}
	if err := h.Handle(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	_, ids, err := db.ListEvidence(id)
	if err != nil || len(ids) != 1 || ids[0] != p.LastAdmin.CopyMessageIDs[0] {
		t.Fatalf("new evidence lost bookkeeping: %v %v", ids, err)
	}
}

// The old stub does not persist cards; integrations above use SQLite.
func (*stubRepo) SaveIncidentCard(int64, store.IncidentCard) error { return nil }
