package blocklist

import "sync/atomic"

// Blocklist holds the current blocklist snapshot behind an atomic pointer so
// a background syncer can swap it race-free while readers (e.g. the
// moderation cascade) look up user IDs concurrently.
//
// (Later tasks add fields for syncing state; this task covers only the
// snapshot holder and lookup.)
type Blocklist struct {
	snap atomic.Pointer[Set]
}

// New returns a Blocklist whose snapshot is a non-nil, empty Set, so Listed
// is safe to call before the first real load.
func New() *Blocklist {
	b := &Blocklist{}
	b.snap.Store(BuildSet())
	return b
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
