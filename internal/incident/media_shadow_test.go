package incident

import (
	"context"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func labelledReview(detail string) domain.Incident {
	inc := historyIncident()
	inc.Verdict = domain.Verdict{
		Action: domain.ActionQuarantine, Reason: "captionless_media", ReviewOnly: true,
		Signals: []domain.Signal{{Name: "captionless_media", Detail: "kinds=photo len=0"}, {Name: "jev_image", Detail: detail}},
	}
	return inc
}

// A certain-looking label must not turn a review card into a sanction: the
// shadow labels, the review verdict decides, and it decides "nothing".
func TestMediaShadowLabelNeverSanctions(t *testing.T) {
	m, p, _ := historySetup(false, nil)
	if err := m.Handle(context.Background(), labelledReview("promo=1.00 contact=1.00 ordinary=0.00")); err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Calls() {
		switch c {
		case "RestrictMember", "BanMember", "BanSenderChat", "DeleteMessages":
			t.Fatalf("labelled review card applied %s: %v", c, p.Calls())
		}
	}
	if len(p.deletes) != 0 {
		t.Fatalf("deletes = %v", p.deletes)
	}
	card := p.LastAdmin.Text
	if !strings.Contains(card, "review only: nothing applied") ||
		!strings.Contains(card, "JEV photo (shadow, no effect): ad 1.00 · contact 1.00 · ordinary 0.00") {
		t.Fatalf("card:\n%s", card)
	}
}

func TestCardShowsMediaShadowLineOnlyWhenParsable(t *testing.T) {
	inc := labelledReview("promo=0.97 contact=0.12 ordinary=0.04")
	got := formatCard(7, inc, "чат", "", false)
	if !strings.Contains(got, "\nJEV photo (shadow, no effect): ad 0.97 · contact 0.12 · ordinary 0.04") {
		t.Fatalf("label missing:\n%s", got)
	}
	inc.Verdict.Signals[1].Detail = "promo=0.97\nchat: forged"
	if got := formatCard(7, inc, "чат", "", false); strings.Contains(got, "JEV") || strings.Contains(got, "forged") {
		t.Fatalf("malformed detail rendered:\n%s", got)
	}
	inc.Verdict.Signals = inc.Verdict.Signals[:1]
	if got := formatCard(7, inc, "чат", "", false); strings.Contains(got, "JEV") {
		t.Fatalf("label without signal:\n%s", got)
	}
}
