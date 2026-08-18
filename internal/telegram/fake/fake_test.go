package fake

import (
	"context"
	"testing"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

func TestFakeImplementsPortAndLogsOrder(t *testing.T) {
	var _ telegram.Port = New()

	f := New()
	f.SendAdminID = 42
	ctx := context.Background()

	ids, err := f.CopyMessages(ctx, 999, -100123, []int{5})
	if err != nil || len(ids) != 1 {
		t.Fatalf("copy: ids=%v err=%v", ids, err)
	}
	id, err := f.SendAdmin(ctx, 999, telegram.AdminMessage{IncidentKey: "k"})
	if err != nil || id != 42 {
		t.Fatalf("sendadmin: id=%d err=%v", id, err)
	}
	if err := f.BanMember(ctx, -100123, 7); err != nil {
		t.Fatal(err)
	}

	got := f.Calls()
	want := []string{"CopyMessages", "SendAdmin", "BanMember"}
	if len(got) != len(want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d = %q want %q", i, got[i], want[i])
		}
	}
}
