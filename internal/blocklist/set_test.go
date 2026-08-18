package blocklist

import "testing"

func TestBuildSetAndContains(t *testing.T) {
	s := BuildSet([]int64{5, 1, 3}, []int64{3, 9})
	if s.Len() != 4 { // {1,3,5,9} deduped
		t.Fatalf("len=%d want 4", s.Len())
	}
	for _, id := range []int64{1, 3, 5, 9} {
		if !s.Contains(id) {
			t.Errorf("Contains(%d)=false want true", id)
		}
	}
	for _, id := range []int64{0, 2, 4, 10} {
		if s.Contains(id) {
			t.Errorf("Contains(%d)=true want false", id)
		}
	}
	var nilSet *Set
	if nilSet.Contains(7) || nilSet.Len() != 0 {
		t.Fatal("nil set must be empty and contain nothing (fail-open)")
	}
	if BuildSet().Len() != 0 {
		t.Fatal("empty build must be len 0")
	}
}
