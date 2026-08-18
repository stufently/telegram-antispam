package detect

import "testing"

func TestDeobfuscate(t *testing.T) {
	cases := map[string]string{
		// (a) zero-width stripping: ZWSP (U+200B) between every letter of "here".
		"h​e​r​e": "here",
		// (b) Cyrillic->Latin fold on a mixed-script word: С and Р are Cyrillic
		// lookalikes for Latin C and P; the rest of the word is already Latin.
		"СRYPTO": "crypto",
		// (c) lowercasing plus a full Cyrillic->Latin fold: а,е,о,р,с,х,к,т,н,м
		// all map onto their Latin lookalikes.
		"АЕОРСХКТНМ": "aeopcxkthm",
		// Greek alpha/omicron folded to Latin a/o, mixed with lowercasing.
		"ΑΟ": "ao",
		// conservative single-letter spacing collapse: "п и ш и" -> "пиши"
		// (each token is a single rune, separated by single spaces).
		"п и ш и": "пиши",
		// spacing collapse must NOT trigger when tokens are multi-letter
		// (letters here are deliberately outside the confusables table too,
		// so this case isolates the spacing rule from the folding rule).
		"лишь для": "лишь для",
	}
	for in, want := range cases {
		if got := Deobfuscate(in); got != want {
			t.Errorf("Deobfuscate(%q)=%q want %q", in, got, want)
		}
	}
}

func TestConfusable(t *testing.T) {
	tests := []struct {
		in     rune
		want   rune
		wantOK bool
	}{
		{'а', 'a', true}, // Cyrillic а (U+0430)
		{'е', 'e', true}, // Cyrillic е (U+0435)
		{'о', 'o', true}, // Cyrillic о (U+043E)
		{'р', 'p', true}, // Cyrillic р (U+0440)
		{'с', 'c', true}, // Cyrillic с (U+0441)
		{'х', 'x', true}, // Cyrillic х (U+0445)
		{'к', 'k', true}, // Cyrillic к (U+043A)
		{'т', 't', true}, // Cyrillic т (U+0442)
		{'н', 'h', true}, // Cyrillic н (U+043D)
		{'м', 'm', true}, // Cyrillic м (U+043C)
		{'α', 'a', true}, // Greek alpha
		{'ο', 'o', true}, // Greek omicron
		{'z', 0, false},  // not in the fold table
	}
	for _, tt := range tests {
		got, ok := Confusable(tt.in)
		if ok != tt.wantOK || (ok && got != tt.want) {
			t.Errorf("Confusable(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}
