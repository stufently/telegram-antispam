// Package detect: this file defines the trust gate — whether a user has sent
// enough meaningful messages in a chat to be exempted from stricter
// new-member checks. The counting itself is store-backed (see
// internal/store/trust.go); this file only holds the pure decision logic and
// the narrow interface it depends on, so detect never imports store.
package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

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

// MeaningfulMinLenDefault is the historical minimum: enough to reject "+"
// and a bare emoji, and nothing more. It is kept as the default so raising
// the bar stays an explicit deployment decision.
const MeaningfulMinLenDefault = 3

// IsMeaningful reports whether a normalized message has real content and
// should count toward a user's trust score. It rejects empty messages,
// whitespace-only messages, and messages shorter than minLen.
//
// minLen is configurable because it decides how cheap it is to WARM UP an
// account. Trust exempts a sender from Bayes, the LLM and the fake-admin
// check, so at the historical minimum of 3 a spammer graduated by typing
// "привет" five times — five words to switch off every expensive detector.
// Raising it buys that back, at the price of leaving genuinely terse members
// untrusted (and therefore checked) for longer.
//
// A non-positive minLen means "unset" and falls back to the default rather
// than accepting everything: a zero here would count an empty message.
// It is pure.
func IsMeaningful(n NormalizedMessage, minLen int) bool {
	if minLen <= 0 {
		minLen = MeaningfulMinLenDefault
	}
	if n.RawLen < minLen {
		return false
	}
	return strings.TrimSpace(n.Text) != ""
}

// TrustFingerprint identifies a message for the "only DIFFERENT messages
// count" rule. It hashes the message's OWN text and nothing else.
//
// Not DupHash: that one runs over the fully normalized blob, which also
// carries the quoted external reply, the poll options and the sender tag —
// so the same "спасибо" typed under two different quoted posts would produce
// two different fingerprints and count twice, which is exactly the loophole
// this closes. The text is lower-cased and deobfuscated first so "Привет" и
// "привeт" (with a Latin e) are one message, not three.
func TrustFingerprint(text string) string {
	sum := sha256.Sum256([]byte(Deobfuscate(strings.ToLower(strings.TrimSpace(text)))))
	return hex.EncodeToString(sum[:])
}
