package detect

import "testing"

func TestLevenshteinWithin(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		want bool
	}{
		{"admin", "admin", 1, true},  // identical
		{"admin", "admln", 1, true},  // 1 substitution
		{"admin", "admins", 1, true}, // 1 insertion
		{"admin", "amin", 1, true},   // 1 deletion
		{"admin", "aXmiY", 1, false}, // 2 edits
		{"admin", "support", 1, false},
		{"Аdmin", "Admin", 1, true}, // cyrillic А vs latin A = 1 sub over runes
		{"", "", 1, true},
		{"a", "", 1, true},
		{"abc", "", 1, false}, // 3 deletions
	}
	for _, c := range cases {
		if got := LevenshteinWithin(c.a, c.b, c.max); got != c.want {
			t.Errorf("LevenshteinWithin(%q,%q,%d)=%v want %v", c.a, c.b, c.max, got, c.want)
		}
	}
}
