package store

import (
	"path/filepath"
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestSanctionSinceRespectsIncidentProgress(t *testing.T) {
	cases := []struct {
		name             string
		state            domain.IncidentState
		action           domain.Action
		dry, claim, want bool
	}{
		{"failed before sanction", domain.StateNotActed, domain.ActionMute, false, false, false},
		{"stopped before evidence", domain.StateEvidenceFailed, domain.ActionMute, false, false, false},
		{"pending mute may be in flight", domain.StatePending, domain.ActionMute, false, false, true},
		{"evidenced ban may be in flight", domain.StateEvidenced, domain.ActionBan, false, false, true},
		{"acted mute", domain.StateActed, domain.ActionMute, false, false, true},
		{"cleaned delete mute", domain.StateCleaned, domain.ActionDeleteMute, false, false, true},
		{"done ban", domain.StateDone, domain.ActionBan, false, false, true},
		{"done dry run", domain.StateDone, domain.ActionBan, true, false, false},
		{"done quarantine", domain.StateDone, domain.ActionQuarantine, false, false, false},
		{"done delete only", domain.StateDone, domain.ActionDeleteOnly, false, false, false},
		{"active override from dry run", domain.StateDone, domain.ActionBan, true, true, true},
		{"active override from quarantine", domain.StateDone, domain.ActionQuarantine, false, true, true},
		{"active override from failed evidence", domain.StateEvidenceFailed, domain.ActionMute, false, true, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(); err != nil {
				t.Fatal(err)
			}
			id, _, err := db.InsertPending(-100, 1, 7, 0, tt.dry, domain.Verdict{Action: tt.action})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.SetIncidentState(id, tt.state); err != nil {
				t.Fatal(err)
			}
			if err := setIncidentTime(db, 7, 1000); err != nil {
				t.Fatal(err)
			}
			if tt.claim {
				claimed, _, _, err := db.ClaimManualOverride(id)
				if err != nil || !claimed {
					t.Fatalf("claim=%v err=%v", claimed, err)
				}
			}
			got, err := db.SanctionSince(-100, 7, 1000)
			if err != nil || got != tt.want {
				t.Fatalf("SanctionSince=%v err=%v, want %v", got, err, tt.want)
			}
			for _, scope := range [][3]int64{{-101, 7, 1000}, {-100, 8, 1000}, {-100, 7, 1001}} {
				got, err := db.SanctionSince(scope[0], scope[1], scope[2])
				if err != nil || got {
					t.Fatalf("wrong scope %v matched: %v %v", scope, got, err)
				}
			}
		})
	}
}
