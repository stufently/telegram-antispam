// Package detect: this file implements obfuscation normalization used to
// defeat common spam tricks — mixed-script confusables, zero-width
// characters inserted mid-word, and letters spaced out one-per-token.
package detect

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// confusables maps a single rune from another script (Cyrillic, Greek, ...)
// to the lowercase Latin letter it is commonly used to impersonate in
// obfuscated spam ("СRYPTO", "аdmin", etc). The table is intentionally
// small and curated: only glyphs that are near-identical to a Latin letter
// at typical Telegram font sizes are included, to avoid mangling genuine
// Cyrillic/Greek text. Keys are lowercase; callers must lowercase input
// (via Deobfuscate) before looking up.
var confusables = map[rune]rune{
	// Cyrillic -> Latin lookalikes.
	'а': 'a', // U+0430 CYRILLIC SMALL LETTER A
	'е': 'e', // U+0435 CYRILLIC SMALL LETTER IE
	'о': 'o', // U+043E CYRILLIC SMALL LETTER O
	'р': 'p', // U+0440 CYRILLIC SMALL LETTER ER
	'с': 'c', // U+0441 CYRILLIC SMALL LETTER ES
	'х': 'x', // U+0445 CYRILLIC SMALL LETTER HA
	'у': 'y', // U+0443 CYRILLIC SMALL LETTER U
	'к': 'k', // U+043A CYRILLIC SMALL LETTER KA
	'т': 't', // U+0442 CYRILLIC SMALL LETTER TE
	'н': 'h', // U+043D CYRILLIC SMALL LETTER EN
	'м': 'm', // U+043C CYRILLIC SMALL LETTER EM
	'і': 'i', // U+0456 CYRILLIC SMALL LETTER BYELORUSSIAN-UKRAINIAN I
	'ѕ': 's', // U+0455 CYRILLIC SMALL LETTER DZE
	'в': 'b', // U+0432 CYRILLIC SMALL LETTER VE (loosely resembles "b")
	// Greek -> Latin lookalikes.
	'α': 'a', // U+03B1 GREEK SMALL LETTER ALPHA
	'ο': 'o', // U+03BF GREEK SMALL LETTER OMICRON
	'ρ': 'p', // U+03C1 GREEK SMALL LETTER RHO
	'κ': 'k', // U+03BA GREEK SMALL LETTER KAPPA
	'υ': 'y', // U+03C5 GREEK SMALL LETTER UPSILON
}

// Confusable reports the Latin fold for r, if r is a known confusable of a
// Latin letter. It is the read-only accessor for the internal fold table.
func Confusable(r rune) (rune, bool) {
	v, ok := confusables[r]
	return v, ok
}

// Invisible runes spammers insert mid-word to break substring matching
// (e.g. "c<ZWSP>r<ZWSP>y<ZWSP>p<ZWSP>t<ZWSP>o"). Written as \u escapes, not
// literal characters, so the source file itself stays free of invisible
// bytes (a literal BOM in particular confuses the Go compiler/tooling).
const (
	zwsp  = '\u200B' // ZERO WIDTH SPACE
	zwnj  = '\u200C' // ZERO WIDTH NON-JOINER
	zwj   = '\u200D' // ZERO WIDTH JOINER
	bom   = '\uFEFF' // ZERO WIDTH NO-BREAK SPACE / BOM
	wordJ = '\u2060' // WORD JOINER
)

func isZeroWidth(r rune) bool {
	switch r {
	case zwsp, zwnj, zwj, bom, wordJ:
		return true
	}
	return false
}

// Deobfuscate normalizes s to defeat common obfuscation tricks used to
// evade keyword/substring spam detection. The steps run in this fixed
// order, each depending on the previous:
//
//  1. NFKC-normalize: folds compatibility variants (full-width, fonts,
//     ligatures, etc) to their canonical form, so later steps see a
//     consistent rune set.
//  2. Lowercase: uppercase confusables (e.g. Cyrillic С) must be folded to
//     the same case before the confusables table (which is keyed on
//     lowercase runes) is consulted.
//  3. Strip zero-width runes: removes invisible word-breaking characters
//     before the spacing-collapse step, so a zero-width rune between two
//     letters does not defeat it (and does not survive into the map lookup
//     stage as its own "token").
//  4. Fold confusables: replace cross-script lookalike runes with the
//     Latin letter they impersonate.
//  5. Collapse single-letter spacing: runs of "<letter> <letter> <letter>"
//     (single rune tokens, separated by exactly one space) are joined,
//     e.g. "п и ш и" -> "пиши". This intentionally does not touch
//     multi-letter tokens or other whitespace/punctuation shapes, to avoid
//     mangling ordinary spaced-out text.
func Deobfuscate(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	s = stripZeroWidth(s)
	s = foldConfusables(s)
	s = collapseSingleLetterSpacing(s)
	return s
}

func stripZeroWidth(s string) string {
	return strings.Map(func(r rune) rune {
		if isZeroWidth(r) {
			return -1
		}
		return r
	}, s)
}

func foldConfusables(s string) string {
	return strings.Map(func(r rune) rune {
		if v, ok := confusables[r]; ok {
			return v
		}
		return r
	}, s)
}

// collapseSingleLetterSpacing joins runs of single-letter tokens separated
// by exactly one ASCII space into a single word, e.g. "п и ш и" -> "пиши".
// It is deliberately conservative: any token longer than one rune, or any
// separator other than a single space, breaks the run and is left as-is.
func collapseSingleLetterSpacing(s string) string {
	tokens := strings.Split(s, " ")
	var b strings.Builder

	for i, tok := range tokens {
		if i > 0 {
			// A single space preceded this token. Emit it unless both the
			// previous and current tokens are single-letter (collapse).
			prevSingle := isSingleLetterToken(tokens[i-1])
			curSingle := isSingleLetterToken(tok)
			if !(prevSingle && curSingle) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(tok)
	}
	return b.String()
}

func isSingleLetterToken(tok string) bool {
	r := []rune(tok)
	return len(r) == 1 && unicode.IsLetter(r[0])
}
