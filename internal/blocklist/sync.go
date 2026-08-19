package blocklist

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// RefreshFull fetches the LOLS full list and the CAS full list and rebuilds
// the snapshot from each source's last-good data.
//
// Fail-open: a failed/empty source retains its own previous successful list;
// a successful source replaces only its own list. If both fetches fail, the
// active snapshot is left untouched. A partial failure still returns an
// informational joined error for logging.
func (b *Blocklist) RefreshFull(ctx context.Context) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()

	var errs []error
	var lols, cas []int64
	var lolsOK, casOK bool

	for _, src := range []struct{ name, url string }{
		{"lols", b.cfg.LolsFullURL},
		{"cas", b.cfg.CasFullURL},
	} {
		ids, err := b.fetch(ctx, src.url)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", src.name, err))
		case len(ids) == 0:
			// A 2xx response that parses to zero ids (e.g. a CDN challenge
			// or maintenance HTML page served with status 200) is treated
			// as a FAILURE, not a successful empty list. The real LOLS/CAS
			// full lists are never empty (millions of ids); letting an empty
			// result through would BuildSet an empty set and silently wipe
			// the last-good snapshot — a total, silent loss of protection.
			// (The 1h delta legitimately can be empty, so this guard lives
			// only in RefreshFull, never in RefreshDelta.)
			errs = append(errs, fmt.Errorf("%s: empty list from %s (treated as failure)", src.name, src.url))
		default:
			switch src.name {
			case "lols":
				lols, lolsOK = ids, true
			case "cas":
				cas, casOK = ids, true
			}
		}
	}

	if !lolsOK && !casOK {
		// Every source failed or returned empty: keep the last-good snapshot.
		return errors.Join(errs...)
	}

	// Store each source sorted+deduplicated rather than as the raw fetched
	// slice: these lists are retained for the process's lifetime (that is the
	// point of per-source last-good state), so paying one sort per refresh to
	// drop duplicates is worth it. No defensive copy is needed — fetch
	// returns a freshly parsed slice that nothing else references, and
	// BuildSet allocates its own backing array.
	if lolsOK {
		b.lolsFull = BuildSet(lols).IDs()
		// A successful full LOLS list supersedes deltas accumulated since the
		// previous successful full refresh.
		b.lolsDelta = nil
	}
	if casOK {
		b.casFull = BuildSet(cas).IDs()
	}
	b.Swap(BuildSet(b.lolsFull, b.lolsDelta, b.casFull))
	return errors.Join(errs...)
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
	if len(b.lolsFull) == 0 && len(b.casFull) == 0 {
		// Lookup-only tests and callers can seed a snapshot through Swap
		// without source attribution; preserve that fallback until a full
		// refresh establishes per-source state.
		b.Swap(BuildSet(b.current().IDs(), delta))
	} else {
		b.Swap(BuildSet(b.lolsFull, b.lolsDelta, b.casFull))
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
