// Package detect: this file defines the trust gate — whether a user has sent
// enough meaningful messages in a chat to be exempted from stricter
// new-member checks. The counting itself is store-backed (see
// internal/store/trust.go); this file only holds the pure decision logic and
// the narrow interface it depends on, so detect never imports store.
package detect

import "strings"

// TrustSource is the read side of the store-backed meaningful-message
// counter. store.DB satisfies this interface.
type TrustSource interface {
	TrustCount(chatID, userID int64) (int, error)
}

// IsTrusted reports whether (chatID, userID) has reached threshold
// meaningful messages. A TrustCount error is treated as not-trusted (fail
// closed), regardless of threshold.
func IsTrusted(src TrustSource, chatID, userID int64, threshold int) bool {
	count, err := src.TrustCount(chatID, userID)
	if err != nil {
		return false
	}
	return count >= threshold
}

// IsMeaningful reports whether a normalized message has real content and
// should count toward a user's trust score. It rejects empty messages,
// whitespace-only messages, and very short non-content tokens (e.g. "+", a
// single emoji) by requiring both a minimum raw length and non-empty
// trimmed text. It is pure.
func IsMeaningful(n NormalizedMessage) bool {
	if n.RawLen < 3 {
		return false
	}
	return strings.TrimSpace(n.Text) != ""
}
