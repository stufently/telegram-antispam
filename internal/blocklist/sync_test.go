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

func TestRefreshFullAndDeltaUnion(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", LolsDeltaURL: "delta"}
	b.fetch = scriptedFetch(map[string][]int64{"lols": {3, 1, 2, 3}, "delta": {3, 4, 5}}, nil)
	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if !b.Listed(id) {
			t.Errorf("Listed(%d)=false want true (union)", id)
		}
	}
	if b.Len() != 5 {
		t.Fatalf("Len()=%d want 5", b.Len())
	}
}

func TestRefreshFullFailureAllowsDelta(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", LolsDeltaURL: "delta"}
	b.fetch = scriptedFetch(map[string][]int64{"delta": {10, 20}}, map[string]error{"lols": errors.New("lols down")})
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected full refresh error")
	}
	if b.Len() != 0 {
		t.Fatal("failed bootstrap populated snapshot")
	}
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !b.Listed(10) || !b.Listed(20) || b.Len() != 2 {
		t.Fatal("delta did not populate snapshot during full-list outage")
	}
}

func TestRefreshFullFailureKeepsLastGoodWhileDeltaAdvances(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", LolsDeltaURL: "delta"}
	b.fetch = scriptedFetch(map[string][]int64{"lols": {1, 2}, "delta": {10, 20}}, nil)
	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.fetch = scriptedFetch(map[string][]int64{"delta": {30}}, map[string]error{"lols": errors.New("lols down")})
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected full refresh error")
	}
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2, 10, 20, 30} {
		if !b.Listed(id) {
			t.Errorf("last-good full/delta snapshot lost id %d", id)
		}
	}
	if b.Len() != 5 {
		t.Fatalf("Len()=%d want 5", b.Len())
	}
}

func TestRefreshFullFailurePreservesAccumulatedDelta(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", LolsDeltaURL: "delta"}
	b.fetch = scriptedFetch(map[string][]int64{"lols": {1}, "delta": {2}}, nil)
	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.fetch = scriptedFetch(nil, map[string]error{"lols": errors.New("lols down")})
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected full refresh error")
	}
	if !b.Listed(1) || !b.Listed(2) || b.Len() != 2 {
		t.Fatal("failed full refresh lost accumulated delta")
	}
}

func TestRefreshFullFailureKeepsLastGood(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols"}
	b.Swap(BuildSet([]int64{111}))
	b.fetch = scriptedFetch(nil, map[string]error{"lols": errors.New("lols down")})
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected full refresh error")
	}
	if !b.Listed(111) || b.Len() != 1 {
		t.Fatal("prior snapshot lost after failed refresh")
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
		LolsDeltaURL:  "delta",
		FullInterval:  time.Hour,
		DeltaInterval: time.Hour,
	}
	b.fetch = scriptedFetch(map[string][]int64{
		"lols": {1, 2},
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

// A 2xx response with zero parsed IDs (e.g. a CDN challenge) must never
// erase the last-good LOLS snapshot.
func TestRefreshFullEmptyKeepsLastGood(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols"}
	b.Swap(BuildSet([]int64{111, 222}))
	b.fetch = scriptedFetch(map[string][]int64{"lols": {}}, nil)
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected error for empty full list")
	}
	if !b.Listed(111) || !b.Listed(222) || b.Len() != 2 {
		t.Fatal("empty full refresh wiped snapshot")
	}
}

func TestRefreshFullEmptyAllowsDeltaThenFullSupersedesDelta(t *testing.T) {
	b := New()
	b.cfg = Config{LolsFullURL: "lols", LolsDeltaURL: "delta"}
	b.fetch = scriptedFetch(map[string][]int64{"lols": {999}, "delta": {7, 8}}, nil)
	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.fetch = scriptedFetch(map[string][]int64{"lols": {}, "delta": {7, 8}}, nil)
	if err := b.RefreshFull(context.Background()); err == nil {
		t.Fatal("expected error for empty full list")
	}
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !b.Listed(999) || !b.Listed(7) || !b.Listed(8) || b.Len() != 3 {
		t.Fatal("delta lost last-good full contribution")
	}
	b.fetch = scriptedFetch(map[string][]int64{"lols": {7, 9}, "delta": {}}, nil)
	if err := b.RefreshFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.RefreshDelta(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !b.Listed(7) || !b.Listed(9) || b.Listed(8) || b.Listed(999) || b.Len() != 2 {
		t.Fatal("successful full refresh failed to supersede old full and delta")
	}
}
