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
