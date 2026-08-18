package blocklist

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// Config holds the URLs and timing parameters the background syncer needs.
type Config struct {
	LolsFullURL   string
	LolsDeltaURL  string
	CasFullURL    string
	FullInterval  time.Duration
	DeltaInterval time.Duration
	HTTPTimeout   time.Duration
}

// fetchFn fetches the id list at url. It is injected on Blocklist so tests
// can script fetch behavior without making real HTTP calls.
type fetchFn func(ctx context.Context, url string) ([]int64, error)

// Blocklist holds the current blocklist snapshot behind an atomic pointer so
// a background syncer can swap it race-free while readers (e.g. the
// moderation cascade) look up user IDs concurrently.
type Blocklist struct {
	snap atomic.Pointer[Set]

	cfg    Config
	fetch  fetchFn
	client *http.Client
}

// New returns a Blocklist whose snapshot is a non-nil, empty Set, so Listed
// is safe to call before the first real load. It has no fetch/config wired
// up; use it for lookup-only tests or NewWithConfig for a syncing instance.
func New() *Blocklist {
	b := &Blocklist{}
	b.snap.Store(BuildSet())
	return b
}

// NewWithConfig returns a Blocklist configured to sync from cfg's URLs. The
// snapshot starts non-nil and empty, exactly like New, until the first
// successful RefreshFull/RefreshDelta (or Run's bootstrap) populates it.
func NewWithConfig(cfg Config) *Blocklist {
	b := &Blocklist{cfg: cfg}
	b.snap.Store(BuildSet())
	b.client = &http.Client{Timeout: cfg.HTTPTimeout}
	b.fetch = func(ctx context.Context, url string) ([]int64, error) {
		return FetchIDs(ctx, b.client, url)
	}
	return b
}

// current returns the currently active snapshot.
func (b *Blocklist) current() *Set {
	return b.snap.Load()
}

// Listed reports whether userID is present in the current snapshot.
// A zero userID always returns false. Fail-open: an empty snapshot returns
// false for every ID. This is the method the cascade's BlocklistSource
// interface calls.
func (b *Blocklist) Listed(userID int64) bool {
	if userID == 0 {
		return false
	}
	return b.snap.Load().Contains(userID)
}

// Swap atomically replaces the current snapshot with s. If s is nil, an
// empty set is stored instead so the snapshot is never nil.
func (b *Blocklist) Swap(s *Set) {
	if s == nil {
		s = BuildSet()
	}
	b.snap.Store(s)
}

// Len returns the size of the current snapshot.
func (b *Blocklist) Len() int {
	return b.snap.Load().Len()
}
