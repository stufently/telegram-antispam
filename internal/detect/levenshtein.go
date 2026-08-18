package detect

// LevenshteinWithin reports whether the rune-based edit distance between a and b is <= max.
// It returns false immediately if the length difference exceeds max.
// Uses a two-row DP and bails as soon as a row's minimum exceeds max.
func LevenshteinWithin(a, b string, max int) bool {
	ar := []rune(a)
	br := []rune(b)

	// Early exit: if length difference exceeds max, edit distance must exceed max
	lenDiff := len(ar) - len(br)
	if lenDiff < 0 {
		lenDiff = -lenDiff
	}
	if lenDiff > max {
		return false
	}

	// Swap so a is the shorter string (optimizes space/time)
	if len(ar) > len(br) {
		ar, br = br, ar
	}

	// Handle case where a is empty
	if len(ar) == 0 {
		return len(br) <= max
	}

	// Two-row DP: prev and curr
	prev := make([]int, len(ar)+1)
	curr := make([]int, len(ar)+1)

	// Initialize prev row (converting empty string to prefix of br)
	for i := 0; i <= len(ar); i++ {
		prev[i] = i
	}

	// Process each character in br
	for j := 1; j <= len(br); j++ {
		curr[0] = j
		minInRow := j

		for i := 1; i <= len(ar); i++ {
			var cost int
			if ar[i-1] == br[j-1] {
				cost = 0
			} else {
				cost = 1
			}

			// Take minimum of: delete (prev[i]+1), insert (curr[i-1]+1), substitute (prev[i-1]+cost)
			curr[i] = min(min(prev[i]+1, curr[i-1]+1), prev[i-1]+cost)

			if curr[i] < minInRow {
				minInRow = curr[i]
			}
		}

		// Early bail: if minimum in this row exceeds max, no future row can be <= max
		if minInRow > max {
			return false
		}

		// Swap rows
		prev, curr = curr, prev
	}

	return prev[len(ar)] <= max
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
