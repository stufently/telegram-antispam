package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// History is the injected interface for recording and counting messages over sliding time windows.
// Implementations maintain sliding-window state for duplicate detection and short-message flooding.
type History interface {
	// RecordAndCountDup records a message hash and returns the count of identical hashes
	// within the given window. The current message is included in the count.
	RecordAndCountDup(chatID, userID int64, textHash string, window time.Duration) int

	// RecentShortCount returns the count of recent short messages from the given user
	// in the given chat within the window. The current short message is included in the count.
	RecentShortCount(chatID, userID int64, window time.Duration) int
}

// BehaviorCfg holds threshold and flag parameters for behavioral detection.
type BehaviorCfg struct {
	DupThreshold        int           // hit when duplicate count >= this (0 = disabled)
	DupWindow           time.Duration // sliding window for duplicate detection
	ShortLen            int           // messages with RawLen <= this are considered short
	ShortFloodThreshold int           // hit when short message count >= this (0 = disabled)
	ShortWindow         time.Duration // sliding window for short message detection
	FlagEdits           bool          // if true, flag edited messages as Signal
}

// DupHash returns a stable SHA256 hex hash of the normalized text.
// Used only for duplicate detection, never for identity or security purposes.
func DupHash(n NormalizedMessage) string {
	hash := sha256.Sum256([]byte(n.Text))
	return hex.EncodeToString(hash[:])
}

// ObserveBehavior records a message into every behavioral window that its
// config enables, without evaluating any threshold.
//
// CheckBehavior cannot be used for observation alone: it is first-hit-wins, so
// an edited message returns before either window is touched and a duplicate
// hit returns before the short-message window is. In the normal path that is
// harmless — a hit means the message is acted on anyway — but a caller that
// must observe a message it will NOT act on (the cascade's admin-lookup
// deferral) needs every window updated, or the burst leaves no trace and stays
// invisible once the lookup recovers.
//
// The enabling conditions mirror CheckBehavior's exactly, so the windows hold
// the same population either way.
func ObserveBehavior(h History, chatID, userID int64, n NormalizedMessage, cfg BehaviorCfg) {
	if h == nil {
		return
	}
	if cfg.DupThreshold > 0 && strings.TrimSpace(n.Text) != "" {
		h.RecordAndCountDup(chatID, userID, DupHash(n), cfg.DupWindow)
	}
	if shortFloodEligible(n, cfg) {
		h.RecentShortCount(chatID, userID, cfg.ShortWindow)
	}
}

// shortFloodEligible reports whether a message counts toward short-message
// flood detection at all.
//
// Captionless media (photo, sticker, voice — RawLen 0 with empty text) is
// excluded for the same reason duplicate detection excludes it: a normal
// user posting a handful of photos one by one is indistinguishable, by
// length alone, from a burst of empty spam, and the sanction for guessing
// wrong is a mute or ban of a real participant. This is the identical trap
// that "image without text" checks in other bots are known for; a length
// heuristic cannot see the picture, so it must not judge it.
func shortFloodEligible(n NormalizedMessage, cfg BehaviorCfg) bool {
	return cfg.ShortFloodThreshold > 0 && n.RawLen <= cfg.ShortLen && strings.TrimSpace(n.Text) != ""
}

// CheckBehavior evaluates behavioral anomalies against the injected History and config.
// Returns a Signal and a boolean indicating whether a behavioral rule was triggered.
// Decision order (first hit wins):
//  1. Edited message (if FlagEdits && edited)
//  2. Duplicate flood
//  3. Short message flood (newcomers only — see below)
//
// trusted marks a user who has cleared the trust threshold. It gates the
// short-message flood check only: rapid-fire short replies ("ok", "+", "ага")
// are ordinary conversation from a regular and a newcomer-spam signal from
// someone who just arrived. Duplicate flood stays ungated — posting the same
// text over and over is spam-shaped no matter how long you have been around.
func CheckBehavior(
	h History,
	chatID, userID int64,
	n NormalizedMessage,
	edited bool,
	trusted bool,
	cfg BehaviorCfg,
) (domain.Signal, bool) {
	// Check edited message first.
	if cfg.FlagEdits && edited {
		return domain.Signal{Name: "edited_message"}, true
	}

	// Check duplicate flood. Skipped entirely when the normalized text is
	// empty/whitespace (captionless media: stickers, photos/voice without a
	// caption). NormalizedMessage carries no file_id, so we cannot hash the
	// media content itself; hashing empty text would collapse every distinct
	// captionless message onto sha256(""), causing RecordAndCountDup to count
	// unrelated messages as duplicates and falsely trigger duplicate_flood.
	// Media-duplicate detection is a future enhancement.
	if cfg.DupThreshold > 0 && strings.TrimSpace(n.Text) != "" {
		dupHash := DupHash(n)
		count := h.RecordAndCountDup(chatID, userID, dupHash, cfg.DupWindow)
		if count >= cfg.DupThreshold {
			return domain.Signal{
				Name:   "duplicate_flood",
				Detail: fmt.Sprintf("%d", count),
			}, true
		}
	}

	// Check short message flood. The window is recorded even for a trusted
	// user, so the population behind it stays identical to ObserveBehavior's
	// (and to what a later untrusted reader would expect); only the verdict
	// is withheld.
	if shortFloodEligible(n, cfg) {
		count := h.RecentShortCount(chatID, userID, cfg.ShortWindow)
		if !trusted && count >= cfg.ShortFloodThreshold {
			return domain.Signal{Name: "short_flood"}, true
		}
	}

	// No behavioral rule triggered.
	return domain.Signal{}, false
}
