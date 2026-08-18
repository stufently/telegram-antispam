package detect

import "testing"

// fakeTrustSource is a test double for TrustSource.
type fakeTrustSource struct {
	counts map[[2]int64]int
	err    error
}

func (f *fakeTrustSource) TrustCount(chatID, userID int64) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.counts[[2]int64{chatID, userID}], nil
}

func TestIsTrustedBelowThreshold(t *testing.T) {
	src := &fakeTrustSource{counts: map[[2]int64]int{{-100, 555}: 2}}
	if IsTrusted(src, -100, 555, 3) {
		t.Fatal("expected not trusted: count 2 < threshold 3")
	}
}

func TestIsTrustedAtThreshold(t *testing.T) {
	src := &fakeTrustSource{counts: map[[2]int64]int{{-100, 555}: 3}}
	if !IsTrusted(src, -100, 555, 3) {
		t.Fatal("expected trusted: count 3 >= threshold 3")
	}
}

func TestIsTrustedAboveThreshold(t *testing.T) {
	src := &fakeTrustSource{counts: map[[2]int64]int{{-100, 555}: 10}}
	if !IsTrusted(src, -100, 555, 3) {
		t.Fatal("expected trusted: count 10 >= threshold 3")
	}
}

func TestIsTrustedErrorTreatedAsNotTrusted(t *testing.T) {
	src := &fakeTrustSource{err: errBoom}
	if IsTrusted(src, -100, 555, 0) {
		t.Fatal("expected not trusted when TrustCount errors, even with threshold 0")
	}
}

func TestIsMeaningfulAcceptsRealText(t *testing.T) {
	n := NormalizedMessage{Text: "hello there, how are you?", RawLen: 26}
	if !IsMeaningful(n) {
		t.Fatal("expected real text to be meaningful")
	}
}

func TestIsMeaningfulRejectsPlusSign(t *testing.T) {
	n := NormalizedMessage{Text: "+", RawLen: 1}
	if IsMeaningful(n) {
		t.Fatal("expected '+' to not be meaningful")
	}
}

func TestIsMeaningfulRejectsEmpty(t *testing.T) {
	n := NormalizedMessage{Text: "", RawLen: 0}
	if IsMeaningful(n) {
		t.Fatal("expected empty message to not be meaningful")
	}
}

func TestIsMeaningfulRejectsWhitespaceOnly(t *testing.T) {
	n := NormalizedMessage{Text: "   ", RawLen: 3}
	if IsMeaningful(n) {
		t.Fatal("expected whitespace-only text to not be meaningful")
	}
}

func TestIsMeaningfulRejectsShortRawLen(t *testing.T) {
	n := NormalizedMessage{Text: "ok", RawLen: 2}
	if IsMeaningful(n) {
		t.Fatal("expected RawLen < 3 to not be meaningful")
	}
}

// errBoom is a sentinel error for tests.
type boomErr struct{}

func (boomErr) Error() string { return "boom" }

var errBoom = boomErr{}
