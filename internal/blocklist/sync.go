package blocklist

import (
	"context"
	"errors"
	"log"
	"time"
)

// RefreshFull fetches the LOLS full list and the CAS full list and rebuilds
// the snapshot from whichever succeed.
//
// Fail-open: if BOTH fetches fail, the snapshot is left untouched (the
// last-good snapshot survives) and the joined error is returned. If at
// least one fetch succeeds, the snapshot is swapped to the union of the
// successful list(s) even if the other failed -- a partial refresh beats no
// refresh. In that partial case the joined error of the failed fetch(es) is
// still returned, but it is informational: the caller should log it, not
// treat it as "nothing happened".
func (b *Blocklist) RefreshFull(ctx context.Context) error {
	var lists [][]int64
	var errs []error

	lolsIDs, lolsErr := b.fetch(ctx, b.cfg.LolsFullURL)
	if lolsErr != nil {
		errs = append(errs, lolsErr)
	} else {
		lists = append(lists, lolsIDs)
	}

	casIDs, casErr := b.fetch(ctx, b.cfg.CasFullURL)
	if casErr != nil {
		errs = append(errs, casErr)
	} else {
		lists = append(lists, casIDs)
	}

	if len(lists) == 0 {
		// Both fetches failed: keep the last-good snapshot.
		return errors.Join(errs...)
	}

	b.Swap(BuildSet(lists...))
	return errors.Join(errs...)
}

// RefreshDelta fetches the LOLS delta list and merges it into the current
// snapshot (union, deduped by BuildSet). On fetch error the snapshot is left
// untouched and the error is returned (fail-open).
func (b *Blocklist) RefreshDelta(ctx context.Context) error {
	delta, err := b.fetch(ctx, b.cfg.LolsDeltaURL)
	if err != nil {
		return err
	}
	b.Swap(BuildSet(b.current().IDs(), delta))
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
