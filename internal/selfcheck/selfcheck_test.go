package selfcheck

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stufently/telegram-antispam/internal/telegram"
)

func TestWarnings(t *testing.T) {
	cases := []struct {
		name string
		in   telegram.BotRights
		want []string
	}{
		{
			name: "full rights, no hazard",
			in:   telegram.BotRights{IsAdmin: true, CanDelete: true, CanRestrict: true},
			want: nil,
		},
		{
			name: "not admin short-circuits per-right flags",
			in:   telegram.BotRights{},
			want: []string{"bot is not an administrator — it cannot delete messages or restrict members"},
		},
		{
			name: "not admin still surfaces aggressive antispam",
			in:   telegram.BotRights{AggressiveAntiSpam: true},
			want: []string{
				"bot is not an administrator — it cannot delete messages or restrict members",
				aggressiveWarn,
			},
		},
		{
			name: "admin missing delete",
			in:   telegram.BotRights{IsAdmin: true, CanRestrict: true},
			want: []string{"missing can_delete_messages"},
		},
		{
			name: "admin missing restrict",
			in:   telegram.BotRights{IsAdmin: true, CanDelete: true},
			want: []string{"missing can_restrict_members"},
		},
		{
			name: "admin missing both plus aggressive",
			in:   telegram.BotRights{IsAdmin: true, AggressiveAntiSpam: true},
			want: []string{"missing can_delete_messages", "missing can_restrict_members", aggressiveWarn},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Warnings(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Warnings(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

type stubChecker struct {
	r   telegram.BotRights
	err error
}

func (s stubChecker) CheckBotRights(context.Context, int64) (telegram.BotRights, error) {
	return s.r, s.err
}

func TestCheck(t *testing.T) {
	msgs, err := Check(context.Background(), stubChecker{r: telegram.BotRights{IsAdmin: true, CanDelete: true, CanRestrict: true}}, -100)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("clean chat: msgs=%v err=%v", msgs, err)
	}
	msgs, err = Check(context.Background(), stubChecker{r: telegram.BotRights{IsAdmin: true, CanRestrict: true}}, -100)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("missing delete: msgs=%v err=%v", msgs, err)
	}
	wantErr := errors.New("boom")
	if _, err := Check(context.Background(), stubChecker{err: wantErr}, -100); !errors.Is(err, wantErr) {
		t.Fatalf("error passthrough: got %v", err)
	}
}
