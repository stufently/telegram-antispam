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
