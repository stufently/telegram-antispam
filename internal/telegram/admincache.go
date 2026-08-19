package telegram

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/stufently/telegram-antispam/internal/detect"
)

var (
	errEmptyAdminList       = errors.New("telegram returned no identifiable chat administrators")
	errAdminListInvalidated = errors.New("chat administrator list was invalidated while being fetched")
	errAdminFetchAborted    = errors.New("chat administrator fetch did not complete")
)

// staleGraceFactor bounds, as a multiple of the TTL, how long a failed
// refetch keeps handing its last good list back alongside the error. Beyond
// it the list is too old to say anything useful and only the error is
// returned.
const staleGraceFactor = 2

// AdminCache is a TTL cache over Port.GetChatAdministrators, mapped to
// detect.AdminIdentity, implementing detect.AdminSource.
//
// On a failed refetch it returns the previous list AND the error, for a
// bounded grace window. Both halves matter, because a stale list is
// asymmetric evidence: it can prove a sender IS an admin — they were one as
// of the last good fetch, and a demotion would have invalidated the entry —
// but it cannot prove that someone absent from it is not an administrator
// promoted since. Callers must honour the positive case and treat the
// negative one as unresolved (spec §4 defers moderation rather than act
// against a possible admin). Returning only the ids would silently authorize
// punitive action on stale data; returning only the error would deny admins
// an immunity the cache can still establish.
//
// While a refetch is failing the cache backs off to at most one attempt per
// TTL per chat, and concurrent misses for the same chat share a single
// in-flight request, so neither an outage nor a burst can turn inbound
// messages into a stream of GetChatAdministrators jobs on the single,
// globally rate-limited dispatcher.
type AdminCache struct {
	port  Port
	ttl   time.Duration
	grace time.Duration
	now   func() time.Time // injectable clock for tests; defaults to time.Now
	ctx   context.Context  // lifecycle context for cache misses; defaults to Background

	mu      sync.Mutex
	entries map[int64]adminEntry
	// gens counts Invalidate calls per chat. A refetch captures the
	// generation before releasing the mutex and refuses to install its result
	// if it changed meanwhile, so an Invalidate issued during an in-flight
	// fetch cannot be undone by that fetch landing afterwards.
	gens map[int64]uint64
	// inflight holds the running refetch per chat so concurrent misses share
	// one request instead of racing: without it two misses both hit the
	// network and the one that happens to finish last installs its result,
	// which may be the older of the two.
	inflight map[int64]*fetchCall
}

// fetchCall is one in-flight refetch that later callers can wait on. ids/err
// are written before done is closed and only read after it, so the close
// provides the happens-before edge.
type fetchCall struct {
	done chan struct{}
	ids  []detect.AdminIdentity
	err  error
}

type adminEntry struct {
	ids       []detect.AdminIdentity
	fetchedAt time.Time
	// lastFailAt/lastErr record the most recent failed refetch, driving both
	// the retry backoff and the error returned with any stale list.
	lastFailAt time.Time
	lastErr    error
}

// stale pairs the entry's remembered failure with its ids while those ids are
// still inside the grace window, and returns the failure alone once they are
// not. The error is always present: stale ids never stand in for a live
// lookup, they only add what the old list can still prove.
func (e adminEntry) stale(now time.Time, grace time.Duration) ([]detect.AdminIdentity, error) {
	if now.Sub(e.fetchedAt) < grace {
		return e.ids, e.lastErr
	}
	return nil, e.lastErr
}

var _ detect.AdminSource = (*AdminCache)(nil)

// NewAdminCache builds an AdminCache that fetches admins from p and caches
// the result per chat for ttl.
func NewAdminCache(p Port, ttl time.Duration) *AdminCache {
	return &AdminCache{
		port:     p,
		ttl:      ttl,
		grace:    staleGraceFactor * ttl,
		now:      time.Now,
		ctx:      context.Background(),
		entries:  map[int64]adminEntry{},
		gens:     map[int64]uint64{},
		inflight: map[int64]*fetchCall{},
	}
}

// SetContext sets the lifecycle context used for Telegram requests on cache
// misses. Call it during startup before the cache becomes visible to workers.
func (c *AdminCache) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
}

// SetClock overrides the cache's clock; for tests only.
func (c *AdminCache) SetClock(now func() time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

// Invalidate drops chatID's cached list and bumps its generation so an
// already in-flight fetch cannot reinstate it. The update router calls this
// for my_chat_member events and for the chat_member events that touch the
// administrator roster, so role changes are visible before the next message
// from that chat instead of waiting for the TTL.
func (c *AdminCache) Invalidate(chatID int64) {
	c.mu.Lock()
	delete(c.entries, chatID)
	c.gens[chatID]++
	c.mu.Unlock()
}

// AdminIdentities implements detect.AdminSource. It returns the cached admin
// identities for chatID, refetching from the port when the cached entry is
// missing or older than the TTL. Network I/O happens without holding the cache
// mutex, so a slow chat cannot block cached lookups for every other chat.
//
// A non-nil error may accompany a non-empty list; see the type doc for what
// that pairing means and how callers must read it.
func (c *AdminCache) AdminIdentities(chatID int64) (ids []detect.AdminIdentity, err error) {
	c.mu.Lock()
	entry, ok := c.entries[chatID]
	now := c.now()
	if ok {
		if now.Sub(entry.fetchedAt) < c.ttl {
			c.mu.Unlock()
			return entry.ids, nil
		}
		if !entry.lastFailAt.IsZero() && now.Sub(entry.lastFailAt) < c.ttl {
			// Backing off from a recent failure: answer from what we have
			// rather than issuing another doomed request.
			ids, err := entry.stale(now, c.grace)
			c.mu.Unlock()
			return ids, err
		}
	}
	if call, running := c.inflight[chatID]; running {
		// Another caller is already refetching this chat; wait for its
		// result instead of starting a second request for the same data.
		c.mu.Unlock()
		<-call.done
		return call.ids, call.err
	}
	call := &fetchCall{done: make(chan struct{})}
	c.inflight[chatID] = call
	ctx := c.ctx
	gen := c.gens[chatID]
	c.mu.Unlock()

	// Seed the failure before anything can go wrong. If the port call panics,
	// the deferred release below still runs, and the named returns are
	// whatever they were at that moment — zero values. Handing waiters
	// (nil, nil) would read as "resolved, and this chat has no admins", which
	// fails OPEN: every sender would sail past the §4 immunity gate into the
	// punitive detectors. An explicit error fails closed instead.
	err = errAdminFetchAborted
	published := false
	defer func() {
		if published {
			return
		}
		// Only reached if the fetch was abandoned. Without this, waiters
		// would park on done forever and every later caller would join the
		// same dead call.
		c.mu.Lock()
		delete(c.inflight, chatID)
		c.mu.Unlock()
		call.ids, call.err = ids, err
		close(call.done)
	}()

	members, fetchErr := c.port.GetChatAdministrators(ctx, chatID)
	if fetchErr == nil && !anyIdentifiable(members) {
		fetchErr = errEmptyAdminList
	}

	// Commit and publish in ONE critical section. Checking the generation,
	// installing the entry, and handing the result to waiters must not be
	// separable: with a gap between them, an Invalidate landing in that gap
	// would correctly drop the cached entry while the leader and every waiter
	// still walked away with the list it just declared wrong, paired with a
	// nil error. That is the one combination the contract must never produce.
	c.mu.Lock()
	ids, err = c.commitLocked(chatID, gen, entry, ok, members, fetchErr)
	delete(c.inflight, chatID)
	call.ids, call.err = ids, err
	close(call.done)
	published = true
	c.mu.Unlock()
	return ids, err
}

// commitLocked resolves one completed fetch against the cache: it decides
// what the caller gets, records the outcome, and must be called with c.mu
// held. prev/hadPrev carry the entry as it looked before the fetch, for the
// stale fallback. A generation bump since the fetch started means an
// Invalidate has declared the result wrong.
func (c *AdminCache) commitLocked(chatID int64, gen uint64, prev adminEntry, hadPrev bool, members []Member, fetchErr error) ([]detect.AdminIdentity, error) {
	invalidated := c.gens[chatID] != gen
	now := c.now()

	if fetchErr != nil {
		if !invalidated {
			// Record the failure even with no cached list (cur is then the
			// zero entry: no ids, and a zero fetchedAt that is outside every
			// grace window). Without this a chat whose very first lookup
			// fails would re-issue GetChatAdministrators for every inbound
			// message for as long as the outage lasts.
			cur := c.entries[chatID]
			cur.lastFailAt, cur.lastErr = now, fetchErr
			c.entries[chatID] = cur
			if hadPrev {
				prev.lastErr = fetchErr
				return prev.stale(now, c.grace)
			}
		}
		// No cached list, or one an Invalidate has since declared wrong.
		return nil, fetchErr
	}

	if invalidated {
		// The result predates a roster change we have already been told
		// about. Neither cache it nor hand it back: §4 defers moderation
		// whenever the admin list cannot be resolved, and a list we know to
		// be wrong resolves nothing. The next call refetches immediately —
		// this is deliberately not negative-cached.
		return nil, errAdminListInvalidated
	}

	ids := make([]detect.AdminIdentity, len(members))
	for i, m := range members {
		ids[i] = detect.AdminIdentity{
			UserID:      m.UserID,
			Username:    m.Username,
			DisplayName: m.DisplayName,
			CustomTitle: m.CustomTitle,
		}
	}
	c.entries[chatID] = adminEntry{ids: ids, fetchedAt: now}
	return ids, nil
}

// anyIdentifiable reports whether the member list contains at least one entry
// with a resolved user id. A list of purely anonymous entries cannot prove
// admin immunity for anyone and is treated as a failed lookup.
func anyIdentifiable(members []Member) bool {
	for _, m := range members {
		if m.UserID != 0 {
			return true
		}
	}
	return false
}
