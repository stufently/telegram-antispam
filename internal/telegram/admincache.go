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
)

// staleGraceFactor bounds, as a multiple of the TTL, how long a failing
// refetch may keep serving the previous admin list. Beyond it the list is too
// old to be trusted and callers get the error instead.
//
// Kept deliberately tight — one extra TTL beyond the entry's normal life,
// which with the one-retry-per-TTL backoff covers a single failed refetch.
// The window is the exposure to the one thing stale data can get wrong that
// the TTL does not already: an administrator promoted without the promotion's
// chat_member event reaching Invalidate would not be recognized as immune.
const staleGraceFactor = 2

// AdminCache is a TTL cache over Port.GetChatAdministrators, mapped to
// detect.AdminIdentity, implementing detect.AdminSource.
//
// A failed refetch does not immediately strip the chat of its admin list:
// every caller treats an error as "cannot prove this sender is not an admin"
// and defers ALL moderation for the chat, so propagating one transient
// Telegram error would disable the blocklist and hard-rule stages too. The
// cache therefore keeps serving the last good list for a short bounded grace
// window (staleGraceFactor x ttl) — the same kind of staleness the TTL already
// permits — and only surfaces the error once the list ages past it. While a
// refetch is failing the cache also backs off to at most one attempt per TTL
// per chat, so an outage cannot turn every inbound message into another
// GetChatAdministrators job on the single, globally rate-limited dispatcher.
type AdminCache struct {
	port  Port
	ttl   time.Duration
	grace time.Duration
	now   func() time.Time // injectable clock for tests; defaults to time.Now
	ctx   context.Context  // lifecycle context for cache misses; defaults to Background

	mu      sync.Mutex
	entries map[int64]adminEntry
	// gens counts Invalidate calls per chat. AdminIdentities captures the
	// generation before releasing the mutex to fetch and refuses to install
	// the result if it changed meanwhile, so an Invalidate issued during an
	// in-flight fetch cannot be undone by that fetch landing afterwards.
	gens map[int64]uint64
}

type adminEntry struct {
	ids       []detect.AdminIdentity
	fetchedAt time.Time
	// lastFailAt/lastErr record the most recent failed refetch, driving both
	// the retry backoff and the error returned once the grace window closes.
	lastFailAt time.Time
	lastErr    error
}

// stale returns the entry's ids if they are still inside the grace window,
// and the remembered failure otherwise.
func (e adminEntry) stale(now time.Time, grace time.Duration) ([]detect.AdminIdentity, error) {
	if now.Sub(e.fetchedAt) < grace {
		return e.ids, nil
	}
	return nil, e.lastErr
}

var _ detect.AdminSource = (*AdminCache)(nil)

// NewAdminCache builds an AdminCache that fetches admins from p and caches
// the result per chat for ttl.
func NewAdminCache(p Port, ttl time.Duration) *AdminCache {
	return &AdminCache{
		port:    p,
		ttl:     ttl,
		grace:   staleGraceFactor * ttl,
		now:     time.Now,
		ctx:     context.Background(),
		entries: map[int64]adminEntry{},
		gens:    map[int64]uint64{},
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
// for chat_member and my_chat_member events so role changes are visible
// before the next message from that chat instead of waiting for the TTL.
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
func (c *AdminCache) AdminIdentities(chatID int64) ([]detect.AdminIdentity, error) {
	c.mu.Lock()
	entry, ok := c.entries[chatID]
	now := c.now()
	ctx := c.ctx
	gen := c.gens[chatID]
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
	c.mu.Unlock()

	members, err := c.port.GetChatAdministrators(ctx, chatID)
	if err == nil && !anyIdentifiable(members) {
		err = errEmptyAdminList
	}
	if err != nil {
		c.mu.Lock()
		now = c.now()
		invalidated := c.gens[chatID] != gen
		if !invalidated {
			// Record the failure even with no cached list (cur is then the
			// zero entry: no ids, and a zero fetchedAt that is outside every
			// grace window). Without this a chat whose very first lookup
			// fails would re-issue GetChatAdministrators for every inbound
			// message for as long as the outage lasts.
			cur := c.entries[chatID]
			cur.lastFailAt, cur.lastErr = now, err
			c.entries[chatID] = cur
		}
		c.mu.Unlock()
		if ok && !invalidated {
			entry.lastErr = err
			return entry.stale(now, c.grace)
		}
		// No cached list, or one an Invalidate has since declared wrong.
		return nil, err
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

	c.mu.Lock()
	stillCurrent := c.gens[chatID] == gen
	if stillCurrent {
		c.entries[chatID] = adminEntry{ids: ids, fetchedAt: c.now()}
	}
	c.mu.Unlock()
	if !stillCurrent {
		// An Invalidate landed while this fetch was in flight, so the result
		// predates a roster change we have already been told about. Neither
		// cache it nor hand it back: §4 defers moderation whenever the admin
		// list cannot be resolved, and a list we know to be wrong resolves
		// nothing. The next call refetches immediately — this is deliberately
		// not negative-cached.
		return nil, errAdminListInvalidated
	}
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
