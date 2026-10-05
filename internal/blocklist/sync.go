package blocklist

import (
	"context"
	"fmt"
	"log"
	"time"
)

// RefreshFull fetches the LOLS full list and replaces the snapshot.
// A failed or empty response preserves the last-good full and delta data.
func (b *Blocklist) RefreshFull(ctx context.Context) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()

	ids, err := b.fetch(ctx, b.cfg.LolsFullURL)
	if err != nil {
		return fmt.Errorf("lols: %w", err)
	}
	if len(ids) == 0 {
		// The full list is never empty: a 2xx CDN challenge/maintenance page
		// must not silently wipe protection. Deltas may legitimately be empty.
		return fmt.Errorf("lols: empty list from %s (treated as failure)", b.cfg.LolsFullURL)
	}

	// Retain sorted, deduplicated data; a successful full list supersedes
	// all deltas accumulated since the previous successful full refresh.
	b.lolsFull = BuildSet(ids).IDs()
	b.lolsDelta = nil
	b.Swap(BuildSet(b.lolsFull))
	return nil
}

// RefreshDelta fetches the LOLS delta list and merges it into the current
// snapshot (union, deduped by BuildSet). On fetch error the snapshot is left
// untouched and the error is returned (fail-open).
func (b *Blocklist) RefreshDelta(ctx context.Context) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()

	delta, err := b.fetch(ctx, b.cfg.LolsDeltaURL)
	if err != nil {
		return err
	}
	b.lolsDelta = BuildSet(b.lolsDelta, delta).IDs()
	if len(b.lolsFull) == 0 {
		// Lookup-only tests and callers can seed a snapshot through Swap
		// without source attribution; preserve that fallback until a full
		// refresh establishes per-source state.
		b.Swap(BuildSet(b.current().IDs(), delta))
	} else {
		b.Swap(BuildSet(b.lolsFull, b.lolsDelta))
	}
	return nil
}

// Run is the background sync loop: it bootstraps with a full refresh, then
// alternates full and delta refreshes on their own tickers until ctx is
// canceled. It never panics; refresh errors are logged, not fatal, so a
// transient outage never blocks the loop or clears a good snapshot
// (fail-open).
func (b *Blocklist) Run(ctx context.Context) {
	if err := b.RefreshFull(ctx); err != nil {
		log.Printf("blocklist bootstrap: %v", err)
	} else {
		log.Printf("blocklist bootstrap: ok, %d ids", b.Len())
	}

	fullTicker := time.NewTicker(b.cfg.FullInterval)
	deltaTicker := time.NewTicker(b.cfg.DeltaInterval)
	defer fullTicker.Stop()
	defer deltaTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-fullTicker.C:
			if err := b.RefreshFull(ctx); err != nil {
				log.Printf("blocklist full refresh: %v", err)
			}
		case <-deltaTicker.C:
			if err := b.RefreshDelta(ctx); err != nil {
				log.Printf("blocklist delta refresh: %v", err)
			}
		}
	}
}
