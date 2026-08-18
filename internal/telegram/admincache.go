package telegram

import (
	"context"
	"sync"
	"time"

	"github.com/stufently/telegram-antispam/internal/detect"
)

// AdminCache is a TTL cache over Port.GetChatAdministrators, mapped to
// detect.AdminIdentity, implementing detect.AdminSource. It fails open: a
// port error never panics or blocks detection — it returns the stale entry
// if one exists, else an empty slice.
type AdminCache struct {
	port Port
	ttl  time.Duration
	now  func() time.Time // injectable clock for tests; defaults to time.Now

	mu      sync.Mutex
	entries map[int64]adminEntry
}

type adminEntry struct {
	ids       []detect.AdminIdentity
	fetchedAt time.Time
}

var _ detect.AdminSource = (*AdminCache)(nil)

// NewAdminCache builds an AdminCache that fetches admins from p and caches
// the result per chat for ttl.
func NewAdminCache(p Port, ttl time.Duration) *AdminCache {
	return &AdminCache{port: p, ttl: ttl, now: time.Now, entries: map[int64]adminEntry{}}
}

// SetClock overrides the cache's clock; for tests only.
func (c *AdminCache) SetClock(now func() time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

// AdminIdentities implements detect.AdminSource. It returns the cached
// admin identities for chatID, refetching from the port when the cached
// entry is missing or older than the TTL.
func (c *AdminCache) AdminIdentities(chatID int64) []detect.AdminIdentity {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[chatID]
	if ok && c.now().Sub(entry.fetchedAt) < c.ttl {
		return entry.ids
	}

	members, err := c.port.GetChatAdministrators(context.Background(), chatID)
	if err != nil {
		if ok {
			return entry.ids
		}
		return nil
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

	c.entries[chatID] = adminEntry{ids: ids, fetchedAt: c.now()}
	return ids
}
