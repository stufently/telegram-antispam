package blocklist

import (
	"context"
	"errors"
	"testing"
	"time"
)

// scriptedFetch builds a fetchFn keyed by URL. ids[url] is returned on
// success; if errs[url] is non-nil, that error is returned instead (and the
// ids for that URL, if any, are ignored).
func scriptedFetch(ids map[string][]int64, errs map[string]error) fetchFn {
	return func(_ context.Context, url string) ([]int64, error) {
		if err := errs[url]; err != nil {
			return nil, err
		}
		return ids[url], nil
	}
}

func TestRefreshFullBothSucceedUnion(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", CasFullURL: "cas"}
	b.fetch = scriptedFetch(map[string][]int64{
		"lols": {1, 2, 3},
		"cas":  {3, 4, 5},
	}, nil)

	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatalf("RefreshFull() error = %v, want nil", err)
	}

	for _, id := range []int64{1, 2, 3, 4, 5} {
		if !b.Listed(id) {
			t.Errorf("Listed(%d) = false, want true (union)", id)
		}
	}
	if b.Len() != 5 {
		t.Fatalf("Len() = %d, want 5", b.Len())
	}
}

func TestRefreshFullOneFailsPartialAppliedWithError(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", CasFullURL: "cas"}
	b.fetch = scriptedFetch(map[string][]int64{
		"cas": {10, 20},
	}, map[string]error{
		"lols": errors.New("lols down"),
	})

	err := b.RefreshFull(context.Background())
	if err == nil {
		t.Fatal("RefreshFull() error = nil, want non-nil (informational, partial failure)")
	}

	if !b.Listed(10) || !b.Listed(20) {
		t.Error("partial snapshot should contain the succeeding source's ids")
	}
	if b.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (only cas ids)", b.Len())
	}
}

func TestRefreshFullPartialFailureKeepsFailedSourceLastGood(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", CasFullURL: "cas"}
	b.fetch = scriptedFetch(map[string][]int64{
		"lols": {1, 2},
		"cas":  {10, 20},
	}, nil)
	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatal(err)
	}

	// CAS advances while LOLS is unavailable. The new CAS list replaces the
	// old CAS contribution, but the last-good LOLS contribution must survive.
	b.fetch = scriptedFetch(map[string][]int64{
		"cas": {30},
	}, map[string]error{
		"lols": errors.New("lols down"),
	})
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected informational partial-refresh error")
	}

	for _, id := range []int64{1, 2, 30} {
		if !b.Listed(id) {
			t.Errorf("last-good partial snapshot lost id %d", id)
		}
	}
	if b.Listed(10) || b.Listed(20) {
		t.Fatal("successful CAS refresh did not replace its own old contribution")
	}
}

func TestRefreshFullPartialFailurePreservesAccumulatedDelta(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", CasFullURL: "cas", LolsDeltaURL: "delta"}
	b.fetch = scriptedFetch(map[string][]int64{
		"lols": {1},
		"cas":  {10},
	}, nil)
	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.fetch = scriptedFetch(map[string][]int64{"delta": {2}}, nil)
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}

	b.fetch = scriptedFetch(map[string][]int64{"cas": {20}}, map[string]error{
		"lols": errors.New("lols down"),
	})
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected informational partial-refresh error")
	}
	for _, id := range []int64{1, 2, 20} {
		if !b.Listed(id) {
			t.Errorf("partial refresh lost id %d", id)
		}
	}
}

func TestRefreshFullBothFailKeepsLastGood(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", CasFullURL: "cas"}
	b.Swap(BuildSet([]int64{111}))

	b.fetch = scriptedFetch(nil, map[string]error{
		"lols": errors.New("lols down"),
		"cas":  errors.New("cas down"),
	})

	err := b.RefreshFull(context.Background())
	if err == nil {
		t.Fatal("RefreshFull() error = nil, want non-nil when both sources fail")
	}

	if !b.Listed(111) {
		t.Error("prior snapshot lost after both-fail refresh; fail-open violated")
	}
	if b.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 (unchanged snapshot)", b.Len())
	}
}

func TestRefreshDeltaMergesIntoCurrent(t *testing.T) {
	b := New()
	b.cfg = Config{LolsDeltaURL: "delta"}
	b.Swap(BuildSet([]int64{1, 2}))

	b.fetch = scriptedFetch(map[string][]int64{
		"delta": {3, 4},
	}, nil)

	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatalf("RefreshDelta() error = %v, want nil", err)
	}

	for _, id := range []int64{1, 2, 3, 4} {
		if !b.Listed(id) {
			t.Errorf("Listed(%d) = false, want true after delta merge", id)
		}
	}
	if b.Len() != 4 {
		t.Fatalf("Len() = %d, want 4", b.Len())
	}
}

func TestRefreshDeltaErrorKeepsSnapshot(t *testing.T) {
	b := New()
	b.cfg = Config{LolsDeltaURL: "delta"}
	b.Swap(BuildSet([]int64{1, 2}))

	b.fetch = scriptedFetch(nil, map[string]error{
		"delta": errors.New("delta down"),
	})

	err := b.RefreshDelta(context.Background())
	if err == nil {
		t.Fatal("RefreshDelta() error = nil, want non-nil")
	}

	if !b.Listed(1) || !b.Listed(2) {
		t.Error("snapshot changed after failed delta refresh; fail-open violated")
	}
	if b.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (unchanged)", b.Len())
	}
}

// TestRunBootstrapsAndStopsOnCancel is a smoke test: Run must not panic, must
// perform a bootstrap RefreshFull, and must return promptly when ctx is
// canceled. It does not assert on ticker firing (that's covered by the
// direct RefreshFull/RefreshDelta tests above).
func TestRunBootstrapsAndStopsOnCancel(t *testing.T) {
	b := New()
	b.cfg = Config{
		LolsFullURL:   "lols",
		CasFullURL:    "cas",
		LolsDeltaURL:  "delta",
		FullInterval:  time.Hour,
		DeltaInterval: time.Hour,
	}
	b.fetch = scriptedFetch(map[string][]int64{
		"lols": {1, 2},
		"cas":  {3},
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after ctx cancel")
	}
}

// TestRefreshFullEmptyBothKeepsLastGood guards the CDN-challenge footgun: a
// 2xx response that parses to zero ids must be treated as a FAILURE, never a
// successful empty list — otherwise both sources returning empty would swap
// in an empty set and silently wipe the last-good snapshot (total, silent
// loss of protection). The real LOLS/CAS full lists are never empty.
func TestRefreshFullEmptyBothKeepsLastGood(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", CasFullURL: "cas"}
	b.Swap(BuildSet([]int64{111, 222}))

	// Both sources succeed at the HTTP level but parse to zero ids.
	b.fetch = scriptedFetch(map[string][]int64{"lols": {}, "cas": {}}, nil)

	err := b.RefreshFull(context.Background())
	if err == nil {
		t.Fatal("RefreshFull() error = nil, want non-nil when both sources return empty")
	}
	if !b.Listed(111) || !b.Listed(222) || b.Len() != 2 {
		t.Fatalf("empty-both refresh wiped snapshot (Len=%d); fail-open violated", b.Len())
	}
}

// TestRefreshFullOneEmptyOtherRealSwapsToReal: one source empty (treated as
// failure), the other returns a real list ⇒ swap to the real one, with an
// informational error for the empty source.
func TestRefreshFullOneEmptyOtherRealSwapsToReal(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", CasFullURL: "cas"}
	b.Swap(BuildSet([]int64{999}))

	b.fetch = scriptedFetch(map[string][]int64{"lols": {}, "cas": {7, 8}}, nil)

	err := b.RefreshFull(context.Background())
	if err == nil {
		t.Fatal("expected informational error for the empty source")
	}
	if !b.Listed(7) || !b.Listed(8) {
		t.Fatal("expected snapshot to contain the real (cas) ids")
	}
	if b.Listed(999) {
		t.Error("prior-only id should be gone after a partial full refresh swap")
	}
}
