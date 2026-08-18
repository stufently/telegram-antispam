package detect

import (
	"net/url"
	"strings"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// Rules defines the hard-rule configuration for detecting spam.
type Rules struct {
	DenyStopwords         []string
	AllowStopwords        []string
	BlockLinksForUntrusted bool
	BannedDomains         []string
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
	// Text is already lowercased from Normalize/Deobfuscate, so we just
	// lowercase the stopword and check if it's a substring.
	text := n.Text

	// First check if any allow stopword is present in the text.
	hasAllow := false
	for _, allow := range r.AllowStopwords {
		if strings.Contains(text, strings.ToLower(allow)) {
			hasAllow = true
			break
		}
	}

	// Then check deny stopwords.
	for _, deny := range r.DenyStopwords {
		if strings.Contains(text, strings.ToLower(deny)) {
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
			Name:   "link_from_untrusted",
			Detail: n.Links[0],
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
	// Try parsing as a full URL with scheme
	u, err := url.Parse(link)
	if err == nil && u.Host != "" {
		return u.Host
	}

	// If parsing failed or no Host in parsed URL, it might be a bare
	// "host/path" form (like "t.me/channel"). Extract the host manually.
	// Split on '/' and take the first part.
	parts := strings.Split(link, "/")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}

	return link
}
