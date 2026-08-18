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

func TestFakeM2Methods(t *testing.T) {
	f := New()
	f.Admins = []telegram.Member{{UserID: 5, Status: "administrator"}}
	ctx := context.Background()
	if err := f.BanSenderChat(ctx, -100123, -100888); err != nil {
		t.Fatal(err)
	}
	admins, err := f.GetChatAdministrators(ctx, -100123)
	if err != nil || len(admins) != 1 || admins[0].UserID != 5 {
		t.Fatalf("admins=%v err=%v", admins, err)
	}
	if err := f.AnswerCallback(ctx, "cb1", "done"); err != nil {
		t.Fatal(err)
	}
	if err := f.EditAdminMarkup(ctx, 999, 7, [][]telegram.Button{{{Text: "x", Data: "d"}}}); err != nil {
		t.Fatal(err)
	}
	got := f.Calls()
	want := []string{"BanSenderChat", "GetChatAdministrators", "AnswerCallback", "EditAdminMarkup"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d=%q want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestFakeDeleteMessageReaction(t *testing.T) {
	f := New()
	ctx := context.Background()
	if err := f.DeleteMessageReaction(ctx, 7, 9, 2); err != nil {
		t.Fatal(err)
	}
	got := f.Calls()
	want := []string{"DeleteMessageReaction"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("calls=%v want=%v", got, want)
	}
	if f.LastReactionDelete.Chat != 7 || f.LastReactionDelete.MessageID != 9 || f.LastReactionDelete.UserID != 2 {
		t.Fatalf("LastReactionDelete=%+v want {Chat:7 MessageID:9 UserID:2}", f.LastReactionDelete)
	}
}

func TestFakeSendEphemeral(t *testing.T) {
	f := New()
	f.EphemeralID = 55
	ctx := context.Background()
	id, err := f.SendEphemeral(ctx, 7, 2, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if id != f.EphemeralID {
		t.Fatalf("id=%d want %d", id, f.EphemeralID)
	}
	got := f.Calls()
	want := []string{"SendEphemeral"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("calls=%v want=%v", got, want)
	}
	wantLast := struct {
		Chat, UserID int64
		Text         string
	}{7, 2, "hi"}
	if f.LastEphemeral != wantLast {
		t.Fatalf("LastEphemeral=%+v want %+v", f.LastEphemeral, wantLast)
	}
}
