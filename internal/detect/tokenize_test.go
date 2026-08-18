package detect

import (
	"testing"
)

func TestTokenize(t *testing.T) {
	n := NormalizedMessage{
		Text:  "buy cheap crypto now",
		Links: []string{"http://spam.test/x"},
		RawLen: 20,
	}
	toks := Tokenize(n)
	has := func(s string) bool { for _, t := range toks { if t == s { return true } }; return false }
	if !has("buy") || !has("crypto") {
		t.Fatalf("unigrams missing: %v", toks)
	}
	if !has("bi:buy_cheap") {
		t.Fatalf("bigram missing: %v", toks)
	}
	if !has("host:spam.test") || !has("has:link") {
		t.Fatalf("link features missing: %v", toks)
	}
}

func TestTokenizeMetaFeatures(t *testing.T) {
	// Test custom emoji feature
	n := NormalizedMessage{
		Text:           "hello world",
		HasCustomEmoji: true,
		RawLen:         11,
	}
	toks := Tokenize(n)
	has := func(s string) bool { for _, t := range toks { if t == s { return true } }; return false }
	if !has("has:custom_emoji") {
		t.Fatalf("has:custom_emoji missing: %v", toks)
	}

	// Test mentions feature
	n = NormalizedMessage{
		Text:      "hello @user",
		Mentions:  []string{"@user"},
		RawLen:    11,
	}
	toks = Tokenize(n)
	if !has("has:mention") {
		t.Fatalf("has:mention missing: %v", toks)
	}

	// Test short length
	n = NormalizedMessage{
		Text:   "hi",
		RawLen: 2,
	}
	toks = Tokenize(n)
	if !has("len:short") {
		t.Fatalf("len:short missing: %v", toks)
	}

	// Test long length
	n = NormalizedMessage{
		Text:   "this is a very long message that exceeds 200 characters in length which is why we should see len:long feature in the token list when we tokenize this message with the tokenizer function we just wrote to handle this case properly",
		RawLen: 200,
	}
	toks = Tokenize(n)
	if !has("len:long") {
		t.Fatalf("len:long missing: %v", toks)
	}
}

func TestTokenizeBigrams(t *testing.T) {
	n := NormalizedMessage{
		Text:   "buy cheap crypto now",
		RawLen: 20,
	}
	toks := Tokenize(n)
	has := func(s string) bool { for _, t := range toks { if t == s { return true } }; return false }

	// Check all expected bigrams
	expectedBigrams := []string{
		"bi:buy_cheap",
		"bi:cheap_crypto",
		"bi:crypto_now",
	}

	for _, bigram := range expectedBigrams {
		if !has(bigram) {
			t.Fatalf("bigram %s missing: %v", bigram, toks)
		}
	}
}

func TestTokenizeMultipleLinks(t *testing.T) {
	n := NormalizedMessage{
		Text:   "visit example.com and spam.test",
		Links:  []string{"http://example.com", "https://spam.test/path"},
		RawLen: 31,
	}
	toks := Tokenize(n)
	has := func(s string) bool { for _, t := range toks { if t == s { return true } }; return false }

	if !has("host:example.com") || !has("host:spam.test") {
		t.Fatalf("host features missing: %v", toks)
	}
	if !has("has:link") {
		t.Fatalf("has:link missing: %v", toks)
	}
}

func TestTokenizeDeterministic(t *testing.T) {
	n := NormalizedMessage{
		Text:   "hello world test",
		RawLen: 16,
	}

	toks1 := Tokenize(n)
	toks2 := Tokenize(n)

	if len(toks1) != len(toks2) {
		t.Fatalf("different lengths: %d vs %d", len(toks1), len(toks2))
	}

	for i := range toks1 {
		if toks1[i] != toks2[i] {
			t.Fatalf("order not deterministic: %v vs %v", toks1, toks2)
		}
	}
}
