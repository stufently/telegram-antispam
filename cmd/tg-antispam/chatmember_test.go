package main

import (
	"context"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/stufently/telegram-antispam/internal/config"
	"github.com/stufently/telegram-antispam/internal/queue"
	"github.com/stufently/telegram-antispam/internal/store"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
	"github.com/stufently/telegram-antispam/internal/watch"
)

func TestPriorityForWelcomeIsLow(t *testing.T) {
	if got := priorityFor("SendWelcome"); got != queue.PrioLow {
		t.Fatalf("SendWelcome priority = %d, want low (%d)", got, queue.PrioLow)
	}
	if got := priorityFor("DeleteMessages"); got != queue.PrioHigh {
		t.Fatalf("DeleteMessages priority = %d, want high (%d)", got, queue.PrioHigh)
	}
	if got := priorityFor("SendEphemeral"); got != queue.PrioNormal {
		t.Fatalf("SendEphemeral priority = %d, want normal (%d)", got, queue.PrioNormal)
	}
}

type orderIdentity struct {
	mu    *sync.Mutex
	order *[]string
}

func (s orderIdentity) UpsertIdentity(chatID, userID int64, username, displayName string) (string, string, bool, error) {
	s.mu.Lock()
	*s.order = append(*s.order, "identity:"+displayName)
	s.mu.Unlock()
	return "", "", false, nil
}

type orderPort struct {
	*fake.Fake
	mu    *sync.Mutex
	order *[]string
}

func (p orderPort) SendWelcome(ctx context.Context, chat, userID int64, text string) (int, error) {
	p.mu.Lock()
	*p.order = append(*p.order, "welcome")
	p.mu.Unlock()
	return p.Fake.SendWelcome(ctx, chat, userID, text)
}

func TestChatMemberUpdateRunsIdentityWatchAndWelcome(t *testing.T) {
	on := true
	max := 20

	run := func(cm models.ChatMemberUpdated) (order []string, port *fake.Fake, invalidated []int64, results []string) {
		t.Helper()
		var mu sync.Mutex
		f := fake.New()
		p := orderPort{Fake: f, mu: &mu, order: &order}
		w := &watch.Welcomer{
			Config: config.NewStore(&config.Config{
				Chats: config.ChatsPolicy{Mode: "auto"},
				Welcome: config.Welcome{
					Enabled:      &on,
					Text:         "rules. mistaken mute: @desk",
					MaxPerMinute: &max,
				},
			}),
			Store: &allowWelcome{},
			Port:  p,
		}
		members := &watch.MemberWatcher{
			Store:   orderIdentity{mu: &mu, order: &order},
			Enabled: false,
		}
		handleChatMember(context.Background(), &cm,
			func(id int64) { invalidated = append(invalidated, id) },
			func(chatID int64, job func()) {
				if chatID != cm.Chat.ID {
					t.Errorf("submitted chat %d, want %d", chatID, cm.Chat.ID)
				}
				job()
			},
			members, w,
			func(result string) { results = append(results, result) },
		)
		return order, f, invalidated, results
	}

	ada := &models.User{ID: 42, FirstName: "Ada", Username: "ada"}
	order, sent, inv, results := run(models.ChatMemberUpdated{
		Chat:          models.Chat{ID: -100, Type: "supergroup"},
		OldChatMember: memberStatus(models.ChatMemberTypeLeft, ada),
		NewChatMember: memberStatus(models.ChatMemberTypeMember, ada),
	})
	if len(inv) != 0 {
		t.Fatalf("ordinary join invalidated %v", inv)
	}
	if len(order) != 2 || order[0] != "identity:Ada" || order[1] != "welcome" {
		t.Fatalf("order = %v, want identity then welcome", order)
	}
	if sent.LastWelcome.Chat != -100 || sent.LastWelcome.UserID != 42 || sent.LastWelcome.Text != "rules. mistaken mute: @desk" {
		t.Fatalf("welcome = %+v", sent.LastWelcome)
	}
	if len(results) != 1 || results[0] != "sent" {
		t.Fatalf("metrics = %v, want [sent]", results)
	}

	renamed := *ada
	renamed.FirstName = "Ann"
	order, sent, inv, results = run(models.ChatMemberUpdated{
		Chat:          models.Chat{ID: -100, Type: "supergroup"},
		OldChatMember: memberStatus(models.ChatMemberTypeMember, ada),
		NewChatMember: memberStatus(models.ChatMemberTypeMember, &renamed),
	})
	if len(inv) != 0 {
		t.Fatalf("rename invalidated %v", inv)
	}
	if len(order) != 1 || order[0] != "identity:Ann" {
		t.Fatalf("rename order = %v, want only the identity write", order)
	}
	if calls := sent.Calls(); len(calls) != 0 {
		t.Fatalf("rename called the port: %v", calls)
	}
	if len(results) != 0 {
		t.Fatalf("rename recorded metrics %v", results)
	}
}

// allowWelcome is a chat that has no row yet and has never been greeted.
type allowWelcome struct{}

func (allowWelcome) WasWelcomed(int64, int64) (bool, error)     { return false, nil }
func (allowWelcome) MarkWelcomed(int64, int64) error            { return nil }
func (allowWelcome) TrustCount(int64, int64) (int, error)       { return 0, nil }
func (allowWelcome) GetChat(int64) (store.ChatRow, bool, error) { return store.ChatRow{}, false, nil }

func memberStatus(kind models.ChatMemberType, u *models.User) models.ChatMember {
	switch kind {
	case models.ChatMemberTypeLeft:
		return models.ChatMember{Type: kind, Left: &models.ChatMemberLeft{Status: kind, User: u}}
	case models.ChatMemberTypeMember:
		return models.ChatMember{Type: kind, Member: &models.ChatMemberMember{Status: kind, User: u}}
	default:
		return models.ChatMember{Type: kind}
	}
}
