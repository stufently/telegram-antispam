package telegram

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestJoinFromChatMemberUpdated(t *testing.T) {
	const chatID int64 = -100555
	person := &models.User{ID: 42, FirstName: "Ada"}
	botUser := &models.User{ID: 99, IsBot: true, FirstName: "Helper"}

	left := asLeft(person)
	kicked := asKicked(person)
	outRestricted := asRestricted(person, false)
	inRestricted := asRestricted(person, true)
	member := asMember(person)
	admin := asAdmin(person)
	owner := asOwner(person)

	tests := []struct {
		name     string
		old, neu models.ChatMember
		via      bool
		want     bool
	}{
		{name: "left to member", old: left, neu: member, want: true},
		{name: "kicked to member", old: kicked, neu: member, want: true},
		{name: "restricted non-member to member", old: outRestricted, neu: member, want: true},
		{name: "left to restricted member", old: left, neu: inRestricted, want: true},
		{name: "kicked to restricted member", old: kicked, neu: inRestricted, want: true},
		{name: "restricted non-member to restricted member", old: outRestricted, neu: inRestricted, want: true},
		{name: "left to member via join request", old: left, neu: member, via: true, want: true},
		{name: "member to member is a rename or no-op", old: member, neu: member, want: false},
		{name: "promotion to administrator", old: member, neu: admin, want: false},
		{name: "promotion to owner", old: admin, neu: owner, want: false},
		{name: "leave", old: member, neu: left, want: false},
		{name: "ban", old: member, neu: kicked, want: false},
		{name: "bot joins", old: asLeft(botUser), neu: asMember(botUser), want: false},
		{name: "restricted member becomes full member", old: inRestricted, neu: member, want: false},
		{name: "left to restricted non-member", old: left, neu: outRestricted, want: false},
		{name: "left straight to administrator", old: left, neu: admin, want: false},
		{name: "kicked to left", old: kicked, neu: left, want: false},
		{name: "restricted non-member promoted", old: outRestricted, neu: admin, want: false},
		{name: "member becomes restricted member", old: member, neu: inRestricted, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := JoinFromChatMemberUpdated(models.ChatMemberUpdated{
				Chat:           models.Chat{ID: chatID, Type: "supergroup"},
				OldChatMember:  tt.old,
				NewChatMember:  tt.neu,
				ViaJoinRequest: tt.via,
			})
			if ok != tt.want {
				t.Fatalf("ok = %v, want %v", ok, tt.want)
			}
			if !tt.want {
				if got != (JoinEvent{}) {
					t.Fatalf("rejected update returned %+v, want the zero event", got)
				}
				return
			}
			if got.ChatID != chatID || got.UserID != person.ID || got.ViaJoinRequest != tt.via {
				t.Fatalf("event = %+v, want chat %d user %d via %v", got, chatID, person.ID, tt.via)
			}
		})
	}
}

func asMember(u *models.User) models.ChatMember {
	return models.ChatMember{
		Type:   models.ChatMemberTypeMember,
		Member: &models.ChatMemberMember{Status: models.ChatMemberTypeMember, User: u},
	}
}

func asLeft(u *models.User) models.ChatMember {
	return models.ChatMember{
		Type: models.ChatMemberTypeLeft,
		Left: &models.ChatMemberLeft{Status: models.ChatMemberTypeLeft, User: u},
	}
}

func asKicked(u *models.User) models.ChatMember {
	return models.ChatMember{
		Type:   models.ChatMemberTypeBanned,
		Banned: &models.ChatMemberBanned{Status: models.ChatMemberTypeBanned, User: u},
	}
}

func asRestricted(u *models.User, isMember bool) models.ChatMember {
	return models.ChatMember{
		Type: models.ChatMemberTypeRestricted,
		Restricted: &models.ChatMemberRestricted{
			Status:   models.ChatMemberTypeRestricted,
			User:     u,
			IsMember: isMember,
		},
	}
}

func asAdmin(u *models.User) models.ChatMember {
	return models.ChatMember{
		Type: models.ChatMemberTypeAdministrator,
		Administrator: &models.ChatMemberAdministrator{
			Status: models.ChatMemberTypeAdministrator,
			User:   *u,
		},
	}
}

func asOwner(u *models.User) models.ChatMember {
	return models.ChatMember{
		Type: models.ChatMemberTypeOwner,
		Owner: &models.ChatMemberOwner{
			Status: models.ChatMemberTypeOwner,
			User:   u,
		},
	}
}
