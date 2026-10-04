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
	// NameMatchSanction restores sanctions for display-name-only matches.
	// The zero value leaves these coincidences for human review.
	NameMatchSanction bool
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

// FakeAdminStrength separates display-name coincidences from stronger evidence.
type FakeAdminStrength uint8

const (
	FakeAdminNoMatch FakeAdminStrength = iota
	FakeAdminNameOnly
	FakeAdminStrong
)

// CheckFakeAdmin preserves the any-match API, including review-only matches.
// The caller guarantees the sender is a non-trusted, non-admin user.
func CheckFakeAdmin(m domain.Message, admins []AdminIdentity, cfg FakeAdminCfg) (domain.Signal, bool) {
	sig, strength := ClassifyFakeAdmin(m, admins, cfg)
	return sig, strength != FakeAdminNoMatch
}

// ClassifyFakeAdmin prefers the first username/custom-title match across all
// admins over a suspicious tag, then the first display-name-only match.
// The caller owns the trust and current-admin immunity gates.
func ClassifyFakeAdmin(m domain.Message, admins []AdminIdentity, cfg FakeAdminCfg) (domain.Signal, FakeAdminStrength) {
	if !cfg.Enabled {
		return domain.Signal{}, FakeAdminNoMatch
	}

	type field struct{ name, value string }
	senderFields := []field{
		{domain.FakeAdminFieldUsername, strings.ToLower(m.Sender.Username)},
		{domain.FakeAdminFieldDisplayName, strings.ToLower(m.Sender.DisplayName)},
	}
	var nameOnly domain.Signal

	for _, admin := range admins {
		adminFields := []field{
			{domain.FakeAdminFieldUsername, strings.ToLower(admin.Username)},
			{domain.FakeAdminFieldCustomTitle, strings.ToLower(admin.CustomTitle)},
			{domain.FakeAdminFieldDisplayName, strings.ToLower(admin.DisplayName)},
		}
		for _, sender := range senderFields {
			if sender.value == "" {
				continue
			}
			for _, adminField := range adminFields {
				if !cfg.nameMatch(sender.value, adminField.value) {
					continue
				}
				sig := domain.Signal{
					Name: "fake_admin",
					Detail: domain.FakeAdminMatchDetail(sender.name, adminField.name,
						admin.UserID, sender.value == adminField.value),
				}
				if adminField.name != domain.FakeAdminFieldDisplayName {
					return sig, FakeAdminStrong
				}
				if nameOnly.Name == "" {
					nameOnly = sig
				}
			}
		}
	}

	senderTag := strings.ToLower(m.SenderTag)
	if senderTag != "" {
		for _, tag := range cfg.SuspiciousTags {
			if senderTag == strings.ToLower(tag) {
				return domain.Signal{Name: "fake_admin", Detail: domain.FakeAdminTagDetail}, FakeAdminStrong
			}
		}
	}

	if nameOnly.Name != "" {
		if cfg.NameMatchSanction {
			return nameOnly, FakeAdminStrong
		}
		return nameOnly, FakeAdminNameOnly
	}
	return domain.Signal{}, FakeAdminNoMatch
}
