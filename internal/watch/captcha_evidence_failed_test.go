package watch

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/store"
)

func TestCaptchaPressAfterEvidenceFailurePasses(t *testing.T) {
	e := newCap(t)
	e.join(7, "Ada")
	id, _, err := e.db.InsertPending(capChat, 1, 7, 0, false, domain.Verdict{Action: domain.ActionMute})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.SetIncidentState(id, domain.StateEvidenceFailed); err != nil {
		t.Fatal(err)
	}
	row := e.row(7)
	if out := e.press(7, row.Attempt, 7); out != CaptchaPassed || e.row(7).State != store.CaptchaPassed {
		t.Fatalf("press=%s state=%s, want passed", out, e.row(7).State)
	}
	if !called(e.calls(), "UnrestrictMember") || e.port.LastUnrestrict.UserID != 7 {
		t.Fatalf("captcha restriction not lifted: %v", e.calls())
	}
}
