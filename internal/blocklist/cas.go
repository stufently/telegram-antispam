package blocklist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Source reports whether a user is on a blocklist.
type Source interface{ Listed(userID int64) bool }

type anyOf []Source

// AnyOf checks sources in order, stopping at the first hit and skipping nils.
func AnyOf(srcs ...Source) Source { return anyOf(append([]Source(nil), srcs...)) }

func (sources anyOf) Listed(userID int64) bool {
	for _, src := range sources {
		if src != nil && src.Listed(userID) {
			return true
		}
	}
	return false
}

const (
	casBreakerErrors = 3
	casBreakerPause  = 60 * time.Second
	casBodyLimit     = 1 << 20
)

// CASConfig controls the per-user CAS check. Nonpositive values use defaults.
type CASConfig struct {
	URL         string        // Default: https://api.cas.chat/check.
	PositiveTTL time.Duration // Default: 24h.
	NegativeTTL time.Duration // Default: 6h.
	Timeout     time.Duration // Default: 2s for the whole request, including its body.
	RatePerSec  float64       // Default: 10.
	Burst       int           // Default: 20.
	MaxEntries  int           // Default: 100000.
}

type casEntry struct {
	listed  bool
	expires time.Time
}

// CASChecker fails open on errors and bounds request frequency and cache size.
// Set optional hooks before calling Listed concurrently. Count may be called
// concurrently and must be safe for that use.
type CASChecker struct {
	Now   func() time.Time // nil means time.Now; used for TTLs, limiter and breaker.
	Count func(result string)
	// Context bounds every request; nil means context.Background. Once it is
	// done, uncached checks return false at once without a request or count,
	// so a shutdown drain is not held up by CAS.
	Context context.Context

	cfg       CASConfig
	client    *http.Client
	limiter   *rate.Limiter
	mu        sync.Mutex
	cache     map[int64]casEntry
	errors    int
	openUntil time.Time
}

// NewCASChecker applies defaults and creates a client if none was supplied.
// The request context enforces Timeout even with a caller-supplied client.
func NewCASChecker(cfg CASConfig, client *http.Client) *CASChecker {
	if cfg.URL == "" {
		cfg.URL = "https://api.cas.chat/check"
	}
	if cfg.PositiveTTL <= 0 {
		cfg.PositiveTTL = 24 * time.Hour
	}
	if cfg.NegativeTTL <= 0 {
		cfg.NegativeTTL = 6 * time.Hour
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.RatePerSec <= 0 {
		cfg.RatePerSec = 10
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 20
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 100000
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &CASChecker{cfg: cfg, client: client, limiter: rate.NewLimiter(rate.Limit(cfg.RatePerSec), cfg.Burst), cache: make(map[int64]casEntry)}
}

func (c *CASChecker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *CASChecker) count(result string) {
	if c.Count != nil {
		c.Count(result)
	}
}

// Listed checks cache, breaker and rate limit before making a bounded request.
// No lock is held over network I/O or the caller's Count hook.
func (c *CASChecker) Listed(userID int64) bool {
	if userID <= 0 {
		return false
	}
	c.mu.Lock()
	now := c.now()
	if entry, ok := c.cache[userID]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.listed
	}
	if c.Context != nil && c.Context.Err() != nil {
		c.mu.Unlock()
		return false
	}
	if now.Before(c.openUntil) {
		c.mu.Unlock()
		c.count("breaker_open")
		return false
	}
	if !c.limiter.AllowN(now, 1) {
		c.mu.Unlock()
		c.count("rate_limited")
		return false
	}
	c.mu.Unlock()

	listed, err := c.check(userID)
	c.mu.Lock()
	now = c.now()
	if err != nil {
		c.errors++
		// Retain the error streak after the pause: one failed probe reopens it.
		// Requests already in flight must not extend an open pause or log again.
		if c.errors >= casBreakerErrors && !now.Before(c.openUntil) {
			c.openUntil = now.Add(casBreakerPause)
			log.Printf("CAS check breaker opened for %s: %v", casBreakerPause, err)
		}
		c.mu.Unlock()
		c.count("error")
		return false
	}
	c.errors = 0
	if !c.openUntil.IsZero() {
		log.Printf("CAS check recovered; breaker closed")
		c.openUntil = time.Time{}
	}
	ttl, result := c.cfg.NegativeTTL, "clean"
	if listed {
		ttl, result = c.cfg.PositiveTTL, "listed"
	}
	if len(c.cache) >= c.cfg.MaxEntries {
		for id, entry := range c.cache {
			if !now.Before(entry.expires) {
				delete(c.cache, id)
			}
		}
	}
	if _, exists := c.cache[userID]; exists || len(c.cache) < c.cfg.MaxEntries {
		c.cache[userID] = casEntry{listed: listed, expires: now.Add(ttl)}
	}
	c.mu.Unlock()
	c.count(result)
	return listed
}

func (c *CASChecker) check(userID int64) (bool, error) {
	parent := c.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, c.cfg.Timeout)
	defer cancel()
	u, err := url.Parse(c.cfg.URL)
	if err != nil {
		return false, fmt.Errorf("invalid CAS check URL: %w", err)
	}
	// Replace any configured query so the sole parameter is the user ID.
	u.RawQuery = url.Values{"user_id": {strconv.FormatInt(userID, 10)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "telegram-antispam-blocklist/1.0 (+https://github.com/stufently/telegram-antispam)")
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("CAS check: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, casBodyLimit))
	if err != nil {
		return false, err
	}
	if len(body) >= casBodyLimit {
		return false, fmt.Errorf("CAS check: response reached %d-byte limit", casBodyLimit)
	}
	var result struct {
		OK *bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("CAS check: invalid JSON: %w", err)
	}
	if result.OK == nil {
		return false, fmt.Errorf("CAS check: missing boolean ok")
	}
	return *result.OK, nil
}
