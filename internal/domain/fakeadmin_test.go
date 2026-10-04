package domain

import (
	"strings"
	"testing"
)

func TestFakeAdminDetailGrammar(t *testing.T) {
	for _, sender := range []string{FakeAdminFieldUsername, FakeAdminFieldDisplayName} {
		for _, admin := range []string{FakeAdminFieldUsername, FakeAdminFieldDisplayName, FakeAdminFieldCustomTitle} {
			for _, exact := range []bool{false, true} {
				detail := FakeAdminMatchDetail(sender, admin, -500, exact)
				kind := "fuzzy"
				if exact {
					kind = "exact"
				}
				want := sender + "~" + admin + " admin_id=-500 " + kind
				if detail != want || !IsFakeAdminDetail(detail) {
					t.Errorf("detail=%q, want valid %q", detail, want)
				}
			}
		}
	}
	for _, valid := range []string{FakeAdminTagDetail, "username~username admin_id=0 exact", "display_name~custom_title admin_id=-12345678901234567890 fuzzy"} {
		if !IsFakeAdminDetail(valid) {
			t.Errorf("rejected %q", valid)
		}
	}
	for _, invalid := range []string{
		"", "name 'x' matches admin 'x'", "Дмитрий", "sender_tag~suspicious_tags\n",
		"username~username admin_id=1 exact\n", "username~username admin_id=1  exact",
		"custom_title~username admin_id=1 exact", "username~sender_tag admin_id=1 exact",
		"username~username admin_id=+1 exact", "username~username admin_id= fuzzy",
		"username~username admin_id=123456789012345678901 fuzzy", "username~username admin_id=١ exact",
		"username~username admin_id=1 approximate", "prefix username~username admin_id=1 exact",
		"username~username admin_id=1 exact suffix", strings.Repeat("x", 1000),
	} {
		if IsFakeAdminDetail(invalid) {
			t.Errorf("accepted unsafe %q", invalid)
		}
	}
}
