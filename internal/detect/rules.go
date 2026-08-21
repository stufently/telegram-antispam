package detect

import (
	"net/url"
	"strings"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// Rules defines the hard-rule configuration for detecting spam.
type Rules struct {
	DenyStopwords          []string
	AllowStopwords         []string
	BlockLinksForUntrusted bool
	BannedDomains          []string
}

// Check applies hard rules to a normalized message and returns the first
// matching signal (deterministic order: deny, link policy, banned domain)
// plus a bool indicating whether a rule matched.
func (r Rules) Check(n NormalizedMessage, trusted bool) (domain.Signal, bool) {
	// 1. Check deny stopwords (unless allow stopwords override)
	if sig, hit := r.checkDenyStopword(n); hit {
		return sig, true
	}

	// 2. Check link policy (BlockLinksForUntrusted && !trusted && has links)
	if sig, hit := r.checkLinkPolicy(n, trusted); hit {
		return sig, true
	}

	// 3. Check banned domains
	if sig, hit := r.checkBannedDomain(n); hit {
		return sig, true
	}

	return domain.Signal{}, false
}

// checkDenyStopword checks if n.Text contains a deny stopword and is not
// overridden by an allow stopword. Returns the deny stopword that matched.
func (r Rules) checkDenyStopword(n NormalizedMessage) (domain.Signal, bool) {
	// n.Text has been through Deobfuscate, so the stopword must go through
	// it too. Lowercasing alone is NOT enough: Deobfuscate folds the twelve
	// Cyrillic letters that look like Latin ones (а е о р с х у к т н м в),
	// so the message text reads "paбota", while a stopword typed in plain
	// Cyrillic still reads "работа" — and strings.Contains never matches.
	// Since nearly every Russian word contains one of those letters, the
	// whole deny/allow list was inert against Cyrillic entries.
	text := n.Text

	// First check if any allow stopword is present in the text.
	hasAllow := false
	for _, allow := range r.AllowStopwords {
		if strings.Contains(text, Deobfuscate(allow)) {
			hasAllow = true
			break
		}
	}

	// Then check deny stopwords.
	for _, deny := range r.DenyStopwords {
		if strings.Contains(text, Deobfuscate(deny)) {
			// If an allow stopword is present, deny does not fire.
			if hasAllow {
				continue
			}
			return domain.Signal{
				Name:   "deny_stopword",
				Detail: strings.ToLower(deny),
			}, true
		}
	}

	return domain.Signal{}, false
}

// checkLinkPolicy checks the link policy rule:
// BlockLinksForUntrusted && !trusted && len(n.Links) > 0
func (r Rules) checkLinkPolicy(n NormalizedMessage, trusted bool) (domain.Signal, bool) {
	if r.BlockLinksForUntrusted && !trusted && len(n.Links) > 0 {
		return domain.Signal{
			Name: "link_from_untrusted",
			// Host only, never the full URL. Signals are serialized into the
			// audit table, which has no retention, so a full link would
			// persist its path and query string indefinitely — and those
			// routinely carry invite codes, referral ids and session tokens
			// belonging to a user who was merely observed. The host is what
			// an admin needs to judge the call.
			Detail: strings.ToLower(extractHost(n.Links[0])),
		}, true
	}
	return domain.Signal{}, false
}

// checkBannedDomain checks if any link's host is in BannedDomains
// (case-insensitive comparison).
func (r Rules) checkBannedDomain(n NormalizedMessage) (domain.Signal, bool) {
	bannedLower := make(map[string]bool)
	for _, d := range r.BannedDomains {
		bannedLower[strings.ToLower(d)] = true
	}

	for _, link := range n.Links {
		host := extractHost(link)
		hostLower := strings.ToLower(host)
		if bannedLower[hostLower] {
			return domain.Signal{
				Name:   "banned_domain",
				Detail: hostLower,
			}, true
		}
	}

	return domain.Signal{}, false
}

// extractHost extracts the host from a URL-like string.
// Handles both "scheme://host/path" form and bare "host/path" form.
func extractHost(link string) string {
	// Try parsing as a full URL with scheme. Hostname(), not Host: the
	// latter keeps the port, so a banned_domains entry "bad.com" would not
	// match "bad.com:8443" — and the same port would ride along into the
	// persisted signal.
	if u, err := url.Parse(link); err == nil && u.Host != "" {
		return u.Hostname()
	}

	// Otherwise it is the bare "host/path" form ("t.me/channel",
	// "bit.ly/x"). Parsing it as "//host/path" makes url.Parse treat the
	// first segment as authority, which trims the path, the query AND the
	// fragment. Splitting on "/" by hand did not: "example.com?invite=code"
	// has no slash, so the whole string — invite code included — was
	// returned as the "host" and stored in the audit row.
	if u, err := url.Parse("//" + link); err == nil && u.Host != "" {
		return u.Hostname()
	}

	return link
}
