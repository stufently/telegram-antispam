package telegram

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestSendCaptchaEphemeralHasKeyboard(t *testing.T) {
	const userID int64 = 7
	var mu sync.Mutex
	var form map[string]string
	p, _, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := formOf(t, r)
		mu.Lock()
		form = f
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":{"message_id":10,"ephemeral_message_id":55,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
	})
	defer stop()
	id, err := p.SendCaptchaEphemeral(context.Background(), -100, userID, "prove", [][]Button{{{Text: "I am not a bot", Data: "cap:-100:7:1"}}})
	mu.Lock()
	defer mu.Unlock()
	if err != nil || id != 55 {
		t.Fatal(id, err)
	}
	assertEphemeralForm(t, form, userID)
	if !strings.Contains(form["reply_markup"], "cap:-100:7:1") {
		t.Fatal(form["reply_markup"])
	}
}

func TestSendCaptchaMessageHasKeyboard(t *testing.T) {
	var mu sync.Mutex
	var form map[string]string
	p, _, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := formOf(t, r)
		mu.Lock()
		form = f
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":{"message_id":77,"date":1,"chat":{"id":-100,"type":"supergroup"}}}`)
	})
	defer stop()
	id, err := p.SendCaptchaMessage(context.Background(), -100, "Ada, prove", [][]Button{{{Text: "Go", Data: "cap:-100:7:2"}}})
	mu.Lock()
	defer mu.Unlock()
	if err != nil || id != 77 {
		t.Fatal(id, err)
	}
	if form["ephemeral_message_parameters"] != "" {
		t.Fatal(form["ephemeral_message_parameters"])
	}
	if !strings.Contains(form["reply_markup"], "cap:-100:7:2") {
		t.Fatal(form["reply_markup"])
	}
}

func TestDeleteEphemeralParams(t *testing.T) {
	var mu sync.Mutex
	var form map[string]string
	var path string
	p, _, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := formOf(t, r)
		mu.Lock()
		form, path = f, r.URL.Path
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":true}`)
	})
	defer stop()
	if err := p.DeleteEphemeral(context.Background(), -100, 7, 55); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.HasSuffix(path, "/deleteEphemeralMessage") {
		t.Fatal(path)
	}
	if form["receiver_user_id"] != "7" || form["ephemeral_message_id"] != "55" || form["chat_id"] != "-100" {
		t.Fatal(form)
	}
}

func TestApproveDeclineJoinRequestParams(t *testing.T) {
	var mu sync.Mutex
	type hit struct {
		path string
		form map[string]string
	}
	var hits []hit
	p, _, stop := startLivePort(t, func(w http.ResponseWriter, r *http.Request) {
		f := formOf(t, r)
		mu.Lock()
		hits = append(hits, hit{path: r.URL.Path, form: f})
		mu.Unlock()
		writeJSON(w, `{"ok":true,"result":true}`)
	})
	defer stop()
	if err := p.ApproveJoinRequest(context.Background(), -100, 7); err != nil {
		t.Fatal(err)
	}
	if err := p.DeclineJoinRequest(context.Background(), -100, 8); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatal(len(hits))
	}
	if !strings.HasSuffix(hits[0].path, "/approveChatJoinRequest") || hits[0].form["chat_id"] != "-100" || hits[0].form["user_id"] != "7" {
		t.Fatal(hits[0])
	}
	if !strings.HasSuffix(hits[1].path, "/declineChatJoinRequest") || hits[1].form["chat_id"] != "-100" || hits[1].form["user_id"] != "8" {
		t.Fatal(hits[1])
	}
}

func TestJoinEventRestrictedFlag(t *testing.T) {
	person := &models.User{ID: 42, FirstName: "Ada"}
	member, ok := JoinFromChatMemberUpdated(models.ChatMemberUpdated{
		Chat: models.Chat{ID: -100, Type: "supergroup"}, OldChatMember: asLeft(person), NewChatMember: asMember(person),
	})
	if !ok || member.Restricted || member.UserID != 42 {
		t.Fatal(member, ok)
	}
	restricted, ok := JoinFromChatMemberUpdated(models.ChatMemberUpdated{
		Chat: models.Chat{ID: -100, Type: "supergroup"}, OldChatMember: asLeft(person), NewChatMember: asRestricted(person, true),
	})
	if !ok || !restricted.Restricted || restricted.UserID != 42 {
		t.Fatal(restricted, ok)
	}
}

func TestMemberChangeFromUpdate(t *testing.T) {
	person := &models.User{ID: 7, FirstName: "Ada"}
	ch, ok := MemberChangeFromUpdate(models.ChatMemberUpdated{
		Chat: models.Chat{ID: -100}, From: models.User{ID: 9}, NewChatMember: asMember(person),
	})
	if !ok || ch.ChatID != -100 || ch.UserID != 7 || ch.ActorID != 9 {
		t.Fatal(ch, ok)
	}
	if _, ok = MemberChangeFromUpdate(models.ChatMemberUpdated{Chat: models.Chat{ID: -100}, NewChatMember: models.ChatMember{Type: models.ChatMemberTypeMember}}); ok {
		t.Fatal("missing user was accepted")
	}
}
