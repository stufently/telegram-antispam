package detect

import (
	"strings"
	"unicode/utf8"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// AdminIdentity holds one current admin's public identifiers for a chat.
type AdminIdentity struct {
	UserID      int64
	Username    string
	DisplayName string
	CustomTitle string
}

// AdminSource returns the admin list for a chat. An error means the caller
// cannot safely determine current-admin immunity; moderation callers must
// defer the decision rather than treating an unknown list as empty.
//
// A non-nil error MAY arrive with a non-empty list — an implementation that
// caches is allowed to hand back its last good list when a refresh fails.
// That pairing is asymmetric evidence and must be read as such:
//
//   - a MATCH on the list is conclusive: the sender was an administrator as
//     of the last successful lookup, so immunity applies;
//   - ABSENCE from it proves nothing, because an administrator promoted since
//     that lookup would be missing. Absent senders are deferred, never passed
//     to a punitive detector on the strength of a list that came with an
//     error.
//
// An implementation that returns ids with an error therefore must not return
// a list it already knows to be wrong (e.g. one superseded by an explicit
// invalidation) — only one that is merely old.
type AdminSource interface {
	AdminIdentities(chatID int64) ([]AdminIdentity, error)
}

// FakeAdminCfg configures the fake-admin (impersonation) detector.
type FakeAdminCfg struct {
	Enabled        bool
	SuspiciousTags []string
	MaxDistance    int
	// MinFuzzyLen is the minimum rune length (of the shorter string) required
	// before fuzzy Levenshtein matching is allowed. Below it, only an exact
	// match counts. Without this floor a distance-1 match on short strings
	// (e.g. "CEO" vs "CFO", or a 3-letter handle vs a 3-letter admin title)
	// produces constant false positives. Default: 5.
	MinFuzzyLen int
}

// nameMatch reports whether sender name a plausibly impersonates admin name b.
// It requires an exact match when either string is shorter than MinFuzzyLen
// (rune count), and otherwise allows up to MaxDistance edits. Empty inputs
// never match.
func (cfg FakeAdminCfg) nameMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if cfg.MaxDistance <= 0 {
		return a == b
	}
	if utf8.RuneCountInString(a) < cfg.MinFuzzyLen || utf8.RuneCountInString(b) < cfg.MinFuzzyLen {
		return a == b
	}
	return LevenshteinWithin(a, b, cfg.MaxDistance)
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

		for _, sender := range []string{senderUsername, senderDisplayName} {
			if sender == "" {
				continue
			}
			for _, adminName := range []string{adminUsername, adminDisplayName, adminCustomTitle} {
				if cfg.nameMatch(sender, adminName) {
					return fakeAdminSignal(sender, adminName), true
				}
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
