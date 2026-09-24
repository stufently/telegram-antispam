package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestJoinFromChatMemberUpdated(t *testing.T) {
	const chatID int64 = -100555
	person := &models.User{ID: 42, FirstName: "Ada"}
	botUser := &models.User{ID: 99, IsBot: true, FirstName: "Helper"}
	left, kicked := asLeft(person), asKicked(person)
	outR, inR := asRestricted(person, false), asRestricted(person, true)
	member, admin, owner := asMember(person), asAdmin(person), asOwner(person)

	tests := []struct {
		name      string
		old, neu  models.ChatMember
		via, want bool
	}{
		{"left to member", left, member, false, true},
		{"kicked to member", kicked, member, false, true},
		{"restricted non-member to member", outR, member, false, true},
		{"left to restricted member", left, inR, false, true},
		{"kicked to restricted member", kicked, inR, false, true},
		{"restricted non-member to restricted member", outR, inR, false, true},
		{"left to member via join request", left, member, true, true},
		{"member to member is a rename", member, member, false, false},
		{"promotion to administrator", member, admin, false, false},
		{"promotion to owner", admin, owner, false, false},
		{"leave", member, left, false, false},
		{"ban", member, kicked, false, false},
		{"bot joins", asLeft(botUser), asMember(botUser), false, false},
		{"restricted member becomes full member", inR, member, false, false},
		{"left to restricted non-member", left, outR, false, false},
		{"left straight to administrator", left, admin, false, false},
		{"kicked to left", kicked, left, false, false},
		{"restricted non-member promoted", outR, admin, false, false},
		{"member becomes restricted member", member, inR, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := JoinFromChatMemberUpdated(models.ChatMemberUpdated{
				Chat:          models.Chat{ID: chatID, Type: "supergroup"},
				OldChatMember: tt.old, NewChatMember: tt.neu, ViaJoinRequest: tt.via,
			})
			if ok != tt.want {
				t.Fatalf("ok=%v, want %v", ok, tt.want)
			}
			if !tt.want {
				if got != (JoinEvent{}) {
					t.Fatalf("rejected event %+v", got)
				}
				return
			}
			if got.ChatID != chatID || got.UserID != person.ID || got.ViaJoinRequest != tt.via {
				t.Fatalf("event=%+v", got)
			}
		})
	}
}

func asMember(u *models.User) models.ChatMember {
	return models.ChatMember{Type: models.ChatMemberTypeMember, Member: &models.ChatMemberMember{Status: models.ChatMemberTypeMember, User: u}}
}
func asLeft(u *models.User) models.ChatMember {
	return models.ChatMember{Type: models.ChatMemberTypeLeft, Left: &models.ChatMemberLeft{Status: models.ChatMemberTypeLeft, User: u}}
}
func asKicked(u *models.User) models.ChatMember {
	return models.ChatMember{Type: models.ChatMemberTypeBanned, Banned: &models.ChatMemberBanned{Status: models.ChatMemberTypeBanned, User: u}}
}
func asRestricted(u *models.User, isMember bool) models.ChatMember {
	return models.ChatMember{Type: models.ChatMemberTypeRestricted, Restricted: &models.ChatMemberRestricted{Status: models.ChatMemberTypeRestricted, User: u, IsMember: isMember}}
}
func asAdmin(u *models.User) models.ChatMember {
	return models.ChatMember{Type: models.ChatMemberTypeAdministrator, Administrator: &models.ChatMemberAdministrator{Status: models.ChatMemberTypeAdministrator, User: *u}}
}
func asOwner(u *models.User) models.ChatMember {
	return models.ChatMember{Type: models.ChatMemberTypeOwner, Owner: &models.ChatMemberOwner{Status: models.ChatMemberTypeOwner, User: u}}
}
