package detect

import (
	"strings"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// AdminIdentity holds one current admin's public identifiers for a chat.
type AdminIdentity struct {
	UserID      int64
	Username    string
	DisplayName string
	CustomTitle string
}

// AdminSource returns the cached admin list for a chat. Implementations
// return an empty slice when the admin list is unknown/uncached — the
// detector treats empty as "no basis" and never panics.
type AdminSource interface {
	AdminIdentities(chatID int64) []AdminIdentity
}

// FakeAdminCfg configures the fake-admin (impersonation) detector.
type FakeAdminCfg struct {
	Enabled        bool
	SuspiciousTags []string
	MaxDistance    int
}

// CheckFakeAdmin flags a non-admin sender whose name is a near-match to a
// current admin's identity, or who carries a suspicious "admin-like" sender
// tag. The caller guarantees the sender is a non-trusted, non-admin user
// (spec §4 immunity), so any near-match to an admin identity — including an
// exact identical string — is treated as impersonation.
func CheckFakeAdmin(m domain.Message, admins []AdminIdentity, cfg FakeAdminCfg) (domain.Signal, bool) {
	if !cfg.Enabled {
		return domain.Signal{}, false
	}

	senderUsername := strings.ToLower(m.Sender.Username)
	senderDisplayName := strings.ToLower(m.Sender.DisplayName)

	for _, admin := range admins {
		adminUsername := strings.ToLower(admin.Username)
		adminDisplayName := strings.ToLower(admin.DisplayName)
		adminCustomTitle := strings.ToLower(admin.CustomTitle)

		if senderUsername != "" {
			if adminUsername != "" && LevenshteinWithin(senderUsername, adminUsername, cfg.MaxDistance) {
				return fakeAdminSignal(senderUsername, adminUsername), true
			}
			if adminDisplayName != "" && LevenshteinWithin(senderUsername, adminDisplayName, cfg.MaxDistance) {
				return fakeAdminSignal(senderUsername, adminDisplayName), true
			}
			if adminCustomTitle != "" && LevenshteinWithin(senderUsername, adminCustomTitle, cfg.MaxDistance) {
				return fakeAdminSignal(senderUsername, adminCustomTitle), true
			}
		}

		if senderDisplayName != "" {
			if adminUsername != "" && LevenshteinWithin(senderDisplayName, adminUsername, cfg.MaxDistance) {
				return fakeAdminSignal(senderDisplayName, adminUsername), true
			}
			if adminDisplayName != "" && LevenshteinWithin(senderDisplayName, adminDisplayName, cfg.MaxDistance) {
				return fakeAdminSignal(senderDisplayName, adminDisplayName), true
			}
			if adminCustomTitle != "" && LevenshteinWithin(senderDisplayName, adminCustomTitle, cfg.MaxDistance) {
				return fakeAdminSignal(senderDisplayName, adminCustomTitle), true
			}
		}
	}

	senderTag := strings.ToLower(m.SenderTag)
	if senderTag != "" {
		for _, tag := range cfg.SuspiciousTags {
			if senderTag == strings.ToLower(tag) {
				return domain.Signal{Name: "fake_admin", Detail: "suspicious sender tag: " + m.SenderTag}, true
			}
		}
	}

	return domain.Signal{}, false
}

func fakeAdminSignal(senderValue, adminValue string) domain.Signal {
	return domain.Signal{
		Name:   "fake_admin",
		Detail: "name '" + senderValue + "' matches admin '" + adminValue + "'",
	}
}
