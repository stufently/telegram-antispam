package blocklist

import (
	"slices"
	"sort"
)

// Set holds a sorted, deduplicated []int64 in ascending order.
// It is immutable once built.
type Set struct {
	ids []int64
}

// BuildSet concatenates all input slices, sorts ascending, deduplicates, and returns a *Set.
// Input slices may be unsorted and overlapping. An empty/no-input build returns a non-nil *Set with Len()==0.
func BuildSet(sources ...[]int64) *Set {
	// Concatenate all sources
	var all []int64
	for _, src := range sources {
		all = append(all, src...)
	}

	// Sort ascending
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	// Deduplicate in-place
	all = slices.Compact(all)

	return &Set{ids: all}
}

// Contains returns true if id is in the set using binary search.
// A nil *Set receiver returns false (fail-open).
func (s *Set) Contains(id int64) bool {
	if s == nil {
		return false
	}
	// Binary search for id in the sorted slice
	idx, found := slices.BinarySearch(s.ids, id)
	_ = idx // idx is unused but BinarySearch returns it
	return found
}

// Len returns the count of elements in the set.
// Returns 0 for a nil receiver.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ids)
}

// IDs returns the sorted, deduplicated ids backing the set. The returned
// slice is READ-ONLY; callers must not mutate it. A nil receiver returns nil.
func (s *Set) IDs() []int64 {
	if s == nil {
		return nil
	}
	return s.ids
}
