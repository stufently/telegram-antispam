package detect

import (
	"testing"
	"time"
)

// fakeHistory is a test double that returns scripted counts for RecordAndCountDup and RecentShortCount.
type fakeHistory struct {
	dupCounts    map[string]int
	shortCounts  int
	recordedDups []string // for verification if needed
	// defaultDupCount is used when a hash is not in dupCounts
	defaultDupCount int
}

func (fh *fakeHistory) RecordAndCountDup(chatID, userID int64, textHash string, window time.Duration) int {
	fh.recordedDups = append(fh.recordedDups, textHash)
	if count, ok := fh.dupCounts[textHash]; ok {
		return count
	}
	return fh.defaultDupCount
}

func (fh *fakeHistory) RecentShortCount(chatID, userID int64, window time.Duration) int {
	return fh.shortCounts
}

func TestCheckBehavior_EditedMessage(t *testing.T) {
	h := &fakeHistory{}
	n := NormalizedMessage{Text: "hello", RawLen: 5}
	cfg := BehaviorCfg{FlagEdits: true, DupThreshold: 0, ShortFloodThreshold: 0}

	signal, hit := CheckBehavior(h, 123, 456, n, true, false, cfg)

	if !hit {
		t.Errorf("expected hit=true for edited message with FlagEdits=true")
	}
	if signal.Name != "edited_message" {
		t.Errorf("expected signal name 'edited_message', got %q", signal.Name)
	}
}

func TestCheckBehavior_EditedMessageIgnoredWhenDisabled(t *testing.T) {
	h := &fakeHistory{}
	n := NormalizedMessage{Text: "hello", RawLen: 5}
	cfg := BehaviorCfg{FlagEdits: false, DupThreshold: 0, ShortFloodThreshold: 0}

	signal, hit := CheckBehavior(h, 123, 456, n, true, false, cfg)

	if hit {
		t.Errorf("expected hit=false for edited message with FlagEdits=false")
	}
	if signal.Name != "" {
		t.Errorf("expected empty signal, got %q", signal.Name)
	}
}

func TestCheckBehavior_DuplicateFlood(t *testing.T) {
	h := &fakeHistory{defaultDupCount: 5}
	n := NormalizedMessage{Text: "duplicate text", RawLen: 14}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        5,
		DupWindow:           1 * time.Minute,
		ShortLen:            10,
		ShortFloodThreshold: 0,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if !hit {
		t.Errorf("expected hit=true for duplicate at threshold")
	}
	if signal.Name != "duplicate_flood" {
		t.Errorf("expected signal name 'duplicate_flood', got %q", signal.Name)
	}
	if signal.Detail == "" {
		t.Errorf("expected non-empty Detail field")
	}
}

func TestCheckBehavior_DuplicateFloodDisabled(t *testing.T) {
	h := &fakeHistory{defaultDupCount: 5}
	n := NormalizedMessage{Text: "duplicate text", RawLen: 14}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        0, // disabled
		DupWindow:           1 * time.Minute,
		ShortLen:            10,
		ShortFloodThreshold: 0,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if hit {
		t.Errorf("expected hit=false when DupThreshold=0 (disabled)")
	}
	if signal.Name != "" {
		t.Errorf("expected empty signal, got %q", signal.Name)
	}
}

func TestCheckBehavior_DuplicateBelowThreshold(t *testing.T) {
	h := &fakeHistory{defaultDupCount: 3}
	n := NormalizedMessage{Text: "duplicate text", RawLen: 14}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        5, // require 5, but count is 3
		DupWindow:           1 * time.Minute,
		ShortLen:            10,
		ShortFloodThreshold: 0,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if hit {
		t.Errorf("expected hit=false when count below threshold")
	}
	if signal.Name != "" {
		t.Errorf("expected empty signal, got %q", signal.Name)
	}
}

func TestCheckBehavior_ShortFlood(t *testing.T) {
	h := &fakeHistory{shortCounts: 10}
	n := NormalizedMessage{Text: "hi", RawLen: 2}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        0,
		ShortLen:            5, // messages with RawLen <= 5 are considered short
		ShortFloodThreshold: 10,
		ShortWindow:         1 * time.Minute,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if !hit {
		t.Errorf("expected hit=true for short flood at threshold")
	}
	if signal.Name != "short_flood" {
		t.Errorf("expected signal name 'short_flood', got %q", signal.Name)
	}
}

func TestCheckBehavior_ShortFloodDisabled(t *testing.T) {
	h := &fakeHistory{shortCounts: 10}
	n := NormalizedMessage{Text: "hi", RawLen: 2}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        0,
		ShortLen:            5,
		ShortFloodThreshold: 0, // disabled
		ShortWindow:         1 * time.Minute,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if hit {
		t.Errorf("expected hit=false when ShortFloodThreshold=0 (disabled)")
	}
	if signal.Name != "" {
		t.Errorf("expected empty signal, got %q", signal.Name)
	}
}

func TestCheckBehavior_ShortFloodBelowThreshold(t *testing.T) {
	h := &fakeHistory{shortCounts: 5}
	n := NormalizedMessage{Text: "hi", RawLen: 2}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        0,
		ShortLen:            5,
		ShortFloodThreshold: 10, // require 10, but count is 5
		ShortWindow:         1 * time.Minute,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if hit {
		t.Errorf("expected hit=false when count below threshold")
	}
	if signal.Name != "" {
		t.Errorf("expected empty signal, got %q", signal.Name)
	}
}

func TestCheckBehavior_NotShort(t *testing.T) {
	h := &fakeHistory{shortCounts: 100}
	n := NormalizedMessage{Text: "this is a longer message", RawLen: 24}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        0,
		ShortLen:            5,
		ShortFloodThreshold: 10,
		ShortWindow:         1 * time.Minute,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if hit {
		t.Errorf("expected hit=false for non-short message")
	}
	if signal.Name != "" {
		t.Errorf("expected empty signal, got %q", signal.Name)
	}
}

func TestCheckBehavior_NoHit(t *testing.T) {
	h := &fakeHistory{shortCounts: 0}
	n := NormalizedMessage{Text: "normal message", RawLen: 14}
	cfg := BehaviorCfg{
		FlagEdits:           false,
		DupThreshold:        5,
		DupWindow:           1 * time.Minute,
		ShortLen:            5,
		ShortFloodThreshold: 10,
		ShortWindow:         1 * time.Minute,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if hit {
		t.Errorf("expected hit=false when no condition matches")
	}
	if signal.Name != "" || signal.Detail != "" {
		t.Errorf("expected empty signal")
	}
}

// TestCheckBehavior_CaptionlessMediaNoDupFlood verifies that captionless
// media (empty NormalizedMessage.Text, e.g. a sticker) never triggers
// duplicate_flood, even if the injected History would report a count at or
// above the threshold. Without the text-empty guard, every distinct
// captionless message hashes to sha256("") and would falsely collapse into
// one "duplicate" bucket, banning unrelated users sending stickers/photos.
func TestCheckBehavior_CaptionlessMediaNoDupFlood(t *testing.T) {
	h := &fakeHistory{defaultDupCount: 3}
	cfg := BehaviorCfg{
		FlagEdits:    false,
		DupThreshold: 3,
		DupWindow:    1 * time.Minute,
	}

	for i := 0; i < 3; i++ {
		n := NormalizedMessage{Text: "", RawLen: 0}
		signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)
		if hit {
			t.Errorf("message %d: expected hit=false for captionless media, got signal %q", i, signal.Name)
		}
	}

	if len(h.recordedDups) != 0 {
		t.Errorf("expected dup check to be skipped entirely for empty text, but RecordAndCountDup was called %d time(s)", len(h.recordedDups))
	}
}

// TestCheckBehavior_RealDuplicateTextStillTriggers is a positive-case
// companion to TestCheckBehavior_CaptionlessMediaNoDupFlood: genuine
// duplicate text (non-empty) must still trigger duplicate_flood.
func TestCheckBehavior_RealDuplicateTextStillTriggers(t *testing.T) {
	h := &fakeHistory{defaultDupCount: 3}
	n := NormalizedMessage{Text: "buy now buy now", RawLen: 16}
	cfg := BehaviorCfg{
		FlagEdits:    false,
		DupThreshold: 3,
		DupWindow:    1 * time.Minute,
	}

	signal, hit := CheckBehavior(h, 123, 456, n, false, false, cfg)

	if !hit {
		t.Errorf("expected hit=true for real duplicate text at threshold")
	}
	if signal.Name != "duplicate_flood" {
		t.Errorf("expected signal name 'duplicate_flood', got %q", signal.Name)
	}
	if len(h.recordedDups) != 1 {
		t.Errorf("expected dup check to run once for non-empty text, got %d calls", len(h.recordedDups))
	}
}

func TestDupHash(t *testing.T) {
	n := NormalizedMessage{Text: "hello world"}
	hash1 := DupHash(n)

	n2 := NormalizedMessage{Text: "hello world"}
	hash2 := DupHash(n2)

	if hash1 != hash2 {
		t.Errorf("expected same hash for same text")
	}

	if hash1 == "" {
		t.Errorf("expected non-empty hash")
	}

	n3 := NormalizedMessage{Text: "different"}
	hash3 := DupHash(n3)

	if hash1 == hash3 {
		t.Errorf("expected different hashes for different texts")
	}
}

// shortCounter counts how many times a short-message event was recorded, so
// tests can tell "not counted" apart from "counted but not acted on".
type shortCounter struct {
	fakeHistory
	shortCalls int
}

func (s *shortCounter) RecentShortCount(chatID, userID int64, window time.Duration) int {
	s.shortCalls++
	return s.shortCounts
}

func TestShortFloodIgnoresCaptionlessMedia(t *testing.T) {
	// A photo/sticker with no caption normalizes to empty text and RawLen 0.
	// Counting it would mean a member posting a handful of pictures one by
	// one trips a flood rule that cannot see the pictures — the same trap
	// "image without text" checks are notorious for.
	h := &shortCounter{fakeHistory: fakeHistory{shortCounts: 99}}
	n := NormalizedMessage{Text: "", RawLen: 0}
	cfg := BehaviorCfg{ShortFloodThreshold: 5, ShortLen: 10, ShortWindow: 30 * time.Second}

	if _, hit := CheckBehavior(h, 1, 2, n, false, false, cfg); hit {
		t.Fatal("captionless media triggered short_flood")
	}
	if h.shortCalls != 0 {
		t.Fatalf("captionless media was recorded into the short window (%d calls)", h.shortCalls)
	}
	// The observe-only path must agree, or the two would disagree about the
	// population behind the window.
	h.shortCalls = 0
	ObserveBehavior(h, 1, 2, n, cfg)
	if h.shortCalls != 0 {
		t.Fatalf("ObserveBehavior recorded captionless media (%d calls)", h.shortCalls)
	}
}

func TestShortFloodOnlyJudgesUntrustedUsers(t *testing.T) {
	n := NormalizedMessage{Text: "ok", RawLen: 2}
	cfg := BehaviorCfg{ShortFloodThreshold: 5, ShortLen: 10, ShortWindow: 30 * time.Second}

	untrusted := &shortCounter{fakeHistory: fakeHistory{shortCounts: 5}}
	sig, hit := CheckBehavior(untrusted, 1, 2, n, false, false, cfg)
	if !hit || sig.Name != "short_flood" {
		t.Fatalf("untrusted burst: hit=%v sig=%q, want short_flood", hit, sig.Name)
	}

	// A regular firing off "ok", "+", "ага" is conversation, not spam.
	trusted := &shortCounter{fakeHistory: fakeHistory{shortCounts: 5}}
	if _, hit := CheckBehavior(trusted, 1, 2, n, false, true, cfg); hit {
		t.Fatal("trusted user flagged for short_flood")
	}
	// The window is still recorded for them, so the population stays the
	// same as ObserveBehavior's; only the verdict is withheld.
	if trusted.shortCalls != 1 {
		t.Fatalf("trusted short message was not recorded (%d calls)", trusted.shortCalls)
	}
}

func TestDuplicateFloodStaysUngatedByTrust(t *testing.T) {
	// Posting the same text over and over is spam-shaped regardless of
	// tenure, so trust must not buy immunity from it.
	h := &fakeHistory{defaultDupCount: 3}
	n := NormalizedMessage{Text: "buy now", RawLen: 7}
	cfg := BehaviorCfg{DupThreshold: 3, DupWindow: time.Minute}

	sig, hit := CheckBehavior(h, 1, 2, n, false, true, cfg)
	if !hit || sig.Name != "duplicate_flood" {
		t.Fatalf("trusted duplicate burst: hit=%v sig=%q, want duplicate_flood", hit, sig.Name)
	}
}
