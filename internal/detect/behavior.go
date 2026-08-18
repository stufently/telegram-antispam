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
	DupThreshold         int           // hit when duplicate count >= this (0 = disabled)
	DupWindow            time.Duration // sliding window for duplicate detection
	ShortLen             int           // messages with RawLen <= this are considered short
	ShortFloodThreshold  int           // hit when short message count >= this (0 = disabled)
	ShortWindow          time.Duration // sliding window for short message detection
	FlagEdits            bool          // if true, flag edited messages as Signal
}

// DupHash returns a stable SHA256 hex hash of the normalized text.
// Used only for duplicate detection, never for identity or security purposes.
func DupHash(n NormalizedMessage) string {
	hash := sha256.Sum256([]byte(n.Text))
	return hex.EncodeToString(hash[:])
}

// CheckBehavior evaluates behavioral anomalies against the injected History and config.
// Returns a Signal and a boolean indicating whether a behavioral rule was triggered.
// Decision order (first hit wins):
//  1. Edited message (if FlagEdits && edited)
//  2. Duplicate flood
//  3. Short message flood
func CheckBehavior(
	h History,
	chatID, userID int64,
	n NormalizedMessage,
	edited bool,
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

	// Check short message flood.
	if cfg.ShortFloodThreshold > 0 && n.RawLen <= cfg.ShortLen {
		count := h.RecentShortCount(chatID, userID, cfg.ShortWindow)
		if count >= cfg.ShortFloodThreshold {
			return domain.Signal{Name: "short_flood"}, true
		}
	}

	// No behavioral rule triggered.
	return domain.Signal{}, false
}
