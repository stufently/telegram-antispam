package admin

import (
	"context"
	"testing"

	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

func TestAuthorizedOperatorAndChatAdmin(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 50}}
	h := NewHandler(f, nil, map[int64]bool{7: true})

	ok, _ := h.Authorized(context.Background(), -100123, 7) // global operator
	if !ok {
		t.Fatal("global operator must be authorized")
	}
	ok, _ = h.Authorized(context.Background(), -100123, 50) // source-chat admin
	if !ok {
		t.Fatal("source-chat admin must be authorized")
	}
	ok, _ = h.Authorized(context.Background(), -100123, 99) // neither
	if ok {
		t.Fatal("non-admin non-operator must be rejected")
	}
}

func TestParseCallbackRoundTrip(t *testing.T) {
	btns := Buttons("abc123")
	// first button data must parse back
	act, key, ok := ParseCallback(btns[0][0].Data)
	if !ok || key != "abc123" || act == "" {
		t.Fatalf("parse: act=%q key=%q ok=%v", act, key, ok)
	}
}
