package main

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/watch"
)

func TestAllowedUpdatesIncludeJoinRequest(t *testing.T) {
	got := strings.Join(allowedUpdates(), ",")
	const want = "message,edited_message,callback_query,chat_member,my_chat_member,message_reaction,chat_join_request"
	if got != want {
		t.Fatal(got)
	}
}

func TestChatJoinRequestRouting(t *testing.T) {
	off := false
	c := &watch.Captcha{
		Config: config.NewStore(&config.Config{
			Chats:   config.ChatsPolicy{Mode: "auto"},
			Captcha: config.Captcha{Enabled: &off},
		}),
	}
	var kinds []string
	var chats []int64
	submit := func(chatID int64, job func()) {
		chats = append(chats, chatID)
		job()
	}
	count := func(kind string) { kinds = append(kinds, kind) }
	req := &models.ChatJoinRequest{
		Chat:       models.Chat{ID: -100},
		From:       models.User{ID: 7, FirstName: "Ada"},
		UserChatID: 700,
	}
	handleChatJoinRequest(context.Background(), req, submit, c, count)
	if len(kinds) != 1 || kinds[0] != "chat_join_request" || len(chats) != 1 || chats[0] != -100 {
		t.Fatal(kinds, chats)
	}
	botReq := &models.ChatJoinRequest{
		Chat:       models.Chat{ID: -100},
		From:       models.User{ID: 9, IsBot: true, FirstName: "Bot"},
		UserChatID: 9,
	}
	handleChatJoinRequest(context.Background(), botReq, func(int64, func()) {
		t.Fatal("bot request was submitted")
	}, c, count)
	if len(kinds) != 2 || kinds[1] != "chat_join_request" {
		t.Fatal(kinds)
	}
}
