package detect

import (
	"testing"

	"github.com/stufently/telegram-antispam/internal/domain"
)

func TestClassifySender(t *testing.T) {
	cases := []struct {
		name string
		in   ClassifyInput
		want domain.SenderKind
	}{
		{"plain user", ClassifyInput{FromID: 7, ChatID: -100123}, domain.SenderUser},
		{"bot", ClassifyInput{FromID: 9, IsBot: true, ChatID: -100123}, domain.SenderBot},
		{"anon admin", ClassifyInput{FromID: AnonAdminBotID, SenderChatID: -100123, SenderChatType: "supergroup", ChatID: -100123}, domain.SenderAnonAdmin},
		{"linked channel autoforward", ClassifyInput{FromID: ServiceNotificationsID, SenderChatID: -100777, SenderChatType: "channel", ChatID: -100123, LinkedChatID: -100777, IsAutomaticForward: true}, domain.SenderLinkedChannel},
		{"external channel", ClassifyInput{SenderChatID: -100888, SenderChatType: "channel", ChatID: -100123, LinkedChatID: -100777}, domain.SenderExternalChannel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifySender(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestAutomaticForwardIsLinkedChannelWithoutLinkedChatID pins the immunity
// that used to be unreachable. Nothing populates LinkedChatID (the Bot API
// does not put it on a message), so requiring the id match classified every
// routine auto-post of a chat's own channel as an external channel — and
// external channels are moderated.
func TestAutomaticForwardIsLinkedChannelWithoutLinkedChatID(t *testing.T) {
	got := ClassifySender(ClassifyInput{
		SenderChatID:       -1001111111111,
		ChatID:             -1002222222222,
		IsAutomaticForward: true,
	})
	if got != domain.SenderLinkedChannel {
		t.Fatalf("got %v, want linked_channel", got)
	}

	// A known-but-different linked chat still wins: that is a forward from
	// somewhere else, not the discussion group's own channel.
	got = ClassifySender(ClassifyInput{
		SenderChatID:       -1001111111111,
		ChatID:             -1002222222222,
		LinkedChatID:       -1003333333333,
		IsAutomaticForward: true,
	})
	if got != domain.SenderExternalChannel {
		t.Fatalf("got %v, want external_channel", got)
	}
}
