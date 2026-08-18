package domain

import "testing"

func TestVerdictIsActionable(t *testing.T) {
	if (Verdict{Action: ActionNone}).IsActionable() {
		t.Error("ActionNone must not be actionable")
	}
	if !(Verdict{Action: ActionDeleteMute}).IsActionable() {
		t.Error("ActionDeleteMute must be actionable")
	}
}

func TestIncidentKeyFields(t *testing.T) {
	inc := Incident{ChatID: -100123, MessageIDs: []int{5, 6}, State: StatePending}
	if inc.ChatID != -100123 || len(inc.MessageIDs) != 2 || inc.State != StatePending {
		t.Fatal("incident fields not wired")
	}
}
