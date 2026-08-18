package telegram

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestRegisteredChatModes(t *testing.T) {
	auto := &config.Config{Chats: config.ChatsPolicy{Mode: "auto"}}
	if !RegisteredChat(auto, -100123) {
		t.Error("auto mode should accept any chat")
	}
	allow := &config.Config{Chats: config.ChatsPolicy{Mode: "allowlist", Allowlist: []int64{-100123}}}
	if !RegisteredChat(allow, -100123) {
		t.Error("allowlisted chat should be accepted")
	}
	if RegisteredChat(allow, -100999) {
		t.Error("non-allowlisted chat should be rejected")
	}
}

func TestImmuneKinds(t *testing.T) {
	if !ImmuneSender(domain.Sender{Kind: domain.SenderAnonAdmin}) {
		t.Error("anon admin is immune")
	}
	if !ImmuneSender(domain.Sender{Kind: domain.SenderLinkedChannel}) {
		t.Error("linked channel is immune")
	}
	if ImmuneSender(domain.Sender{Kind: domain.SenderUser}) {
		t.Error("plain user is not immune")
	}
}
