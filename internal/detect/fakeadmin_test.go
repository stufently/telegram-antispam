package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestCheckFakeAdmin(t *testing.T) {
	admins := []AdminIdentity{{Username: "realadmin", DisplayName: "Group Owner", CustomTitle: "Founder"}}
	cfg := FakeAdminCfg{Enabled: true, SuspiciousTags: []string{"admin", "support"}, MaxDistance: 1}

	// Near-match username (1 edit) → flagged.
	m := domain.Message{Sender: domain.Sender{Username: "realadm1n", DisplayName: "nobody"}}
	if sig, hit := CheckFakeAdmin(m, admins, cfg); !hit || sig.Name != "fake_admin" {
		t.Fatalf("near-match username should flag: hit=%v sig=%+v", hit, sig)
	}

	// Suspicious sender tag on a plain user → flagged.
	m2 := domain.Message{Sender: domain.Sender{Username: "randomguy", DisplayName: "Random"}, SenderTag: "Admin"}
	if _, hit := CheckFakeAdmin(m2, admins, cfg); !hit {
		t.Fatal("suspicious sender tag should flag")
	}

	// Unrelated user, benign tag → not flagged.
	m3 := domain.Message{Sender: domain.Sender{Username: "alice", DisplayName: "Alice"}, SenderTag: "Member"}
	if _, hit := CheckFakeAdmin(m3, admins, cfg); hit {
		t.Fatal("unrelated user must not flag")
	}

	// Disabled → never flags.
	if _, hit := CheckFakeAdmin(m, admins, FakeAdminCfg{Enabled: false, MaxDistance: 1}); hit {
		t.Fatal("disabled must not flag")
	}

	// No admins known + benign tag → no basis, no flag.
	if _, hit := CheckFakeAdmin(m, nil, cfg); hit {
		t.Fatal("no admin list + no suspicious tag must not flag")
	}
}

func TestCheckFakeAdminMinFuzzyLenFloor(t *testing.T) {
	// Short admin title "CEO" (3 runes) vs sender "CFO": distance 1.
	admins := []AdminIdentity{{CustomTitle: "CEO"}}
	sender := domain.Message{Sender: domain.Sender{DisplayName: "CFO"}}

	// No floor (MinFuzzyLen 0): the short-string distance-1 false positive fires.
	noFloor := FakeAdminCfg{Enabled: true, MaxDistance: 1}
	if _, hit := CheckFakeAdmin(sender, admins, noFloor); !hit {
		t.Fatal("without a floor, CEO~CFO should match (baseline)")
	}

	// With the default floor (5), short names require an exact match → no flag.
	withFloor := FakeAdminCfg{Enabled: true, MaxDistance: 1, MinFuzzyLen: 5}
	if _, hit := CheckFakeAdmin(sender, admins, withFloor); hit {
		t.Fatal("floor must suppress the CEO~CFO short-string false positive")
	}

	// The floor must NOT suppress a genuine impersonation of a long name.
	longAdmins := []AdminIdentity{{Username: "moderator"}}
	impostor := domain.Message{Sender: domain.Sender{Username: "moderatr"}} // distance 1, len >= 5
	if _, hit := CheckFakeAdmin(impostor, longAdmins, withFloor); !hit {
		t.Fatal("floor must still allow fuzzy match on long names")
	}

	// An exact short-name impersonation still flags even with the floor.
	exact := domain.Message{Sender: domain.Sender{DisplayName: "CEO"}}
	if _, hit := CheckFakeAdmin(exact, admins, withFloor); !hit {
		t.Fatal("exact short-name match must still flag under the floor")
	}
}
