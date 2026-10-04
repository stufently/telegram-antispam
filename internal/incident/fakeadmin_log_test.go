package incident

import (
	"errors"
	"strings"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

func TestFakeAdminDetailInEnforceLog(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{{Name: "fake_admin", Detail: "username~username admin_id=42 fuzzy"}}
	out := handled(t, fake.New(), inc)
	if !strings.Contains(out, "enforced incident=") || !strings.Contains(out, `[fake_admin] fake_admin_match="username~username admin_id=42 fuzzy"`) || strings.Count(out, "\n") != 1 {
		t.Fatalf("missing safe detail on a single enforcement line: %q", out)
	}
}

func TestFakeAdminLogSelectsFirstSafeDetailBeforeError(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{
		{Name: "other", Detail: "username~username admin_id=1 exact"},
		{Name: "fake_admin", Detail: "unsafe\nname"},
		{Name: "fake_admin", Detail: "sender_tag~suspicious_tags"},
		{Name: "fake_admin", Detail: "username~username admin_id=2 exact"},
	}
	out := handled(t, &fake.Fake{DeleteErr: errors.New("failure")}, inc)
	if !strings.Contains(out, `[other fake_admin fake_admin fake_admin] fake_admin_match="sender_tag~suspicious_tags": delete originals: failure`) || strings.Count(out, "fake_admin_match=") != 1 || strings.Count(out, "\n") != 1 {
		t.Fatalf("wrong detail selection or error ordering: %q", out)
	}
}

func TestFakeAdminCardSanitizesAndClipsFirstNonemptyDetail(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{
		{Name: "other", Detail: "ignored"},
		{Name: "fake_admin"},
		{Name: "fake_admin", Detail: "old\n\t\u202ename " + strings.Repeat("я", 300)},
		{Name: "fake_admin", Detail: "second detail"},
	}
	out := formatCard(1, inc, "", "", true)
	want := "\nmatch: old  name " + strings.Repeat("я", 190) + "…"
	if !strings.HasSuffix(out, want) || strings.Count(out, "\n") != 3 {
		t.Fatalf("card must sanitize and bound its first nonempty match: %q", out)
	}
}

func TestFakeAdminDetailInReviewLog(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.ReviewOnly = true
	inc.Verdict.Action = domain.ActionQuarantine
	inc.Verdict.Signals = []domain.Signal{{Name: "fake_admin", Detail: "display_name~display_name admin_id=500 fuzzy"}}
	f := fake.New()
	out := handled(t, f, inc)
	for _, want := range []string{"not enforced", "review_only=true", `fake_admin_match="display_name~display_name admin_id=500 fuzzy"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	for _, call := range f.Calls() {
		switch call {
		case "BanMember", "RestrictMember", "BanSenderChat", "DeleteMessages":
			t.Errorf("review must not sanction or delete: %v", f.Calls())
		}
	}
}

func TestUnsafeFakeAdminDetailNotLogged(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{{Name: "fake_admin", Detail: "name 'Дмитрий' matches admin 'Дмитрий'"}}
	out := handled(t, fake.New(), inc)
	if strings.Contains(out, "Дмитрий") || strings.Contains(out, "fake_admin_match=") {
		t.Fatalf("unsafe detail reached log: %q", out)
	}
}

func TestCardShowsFakeAdminMatch(t *testing.T) {
	inc := liveIncident(false)
	inc.Verdict.Signals = []domain.Signal{{Name: "fake_admin", Detail: "display_name~display_name admin_id=500 fuzzy"}}
	out := formatCard(1, inc, "chat", "", true)
	if !strings.Contains(out, "\nfrom: id=7\nmatch: display_name~display_name admin_id=500 fuzzy") {
		t.Fatalf("card lacks match after sender: %q", out)
	}
	inc.Verdict.Signals = nil
	if out := formatCard(1, inc, "chat", "", true); strings.Contains(out, "match:") {
		t.Fatalf("card without fake_admin has match line: %q", out)
	}
}
