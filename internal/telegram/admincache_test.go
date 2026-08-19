package telegram_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/telegram"
	"github.com/stufently/telegram-antispam/internal/telegram/fake"
)

func TestAdminCacheMapsAndCaches(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{
		{UserID: 1, Username: "a", CustomTitle: "Boss"},
		{UserID: 2, DisplayName: "Bob"},
	}
	c := telegram.NewAdminCache(f, time.Minute)

	now := time.Now()
	c.SetClock(func() time.Time { return now })

	ids, err := c.AdminIdentities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 identities, got %d", len(ids))
	}
	if ids[0].UserID != 1 || ids[0].Username != "a" || ids[0].CustomTitle != "Boss" {
		t.Fatalf("unexpected first identity: %+v", ids[0])
	}
	if ids[1].UserID != 2 || ids[1].DisplayName != "Bob" {
		t.Fatalf("unexpected second identity: %+v", ids[1])
	}

	if calls := countCalls(f, "GetChatAdministrators"); calls != 1 {
		t.Fatalf("expected 1 GetChatAdministrators call, got %d", calls)
	}

	// Second call within TTL: cached, no refetch.
	if _, err := c.AdminIdentities(10); err != nil {
		t.Fatal(err)
	}
	if calls := countCalls(f, "GetChatAdministrators"); calls != 1 {
		t.Fatalf("expected still 1 GetChatAdministrators call (cached), got %d", calls)
	}

	// Advance past TTL: refetch.
	later := now.Add(2 * time.Minute)
	c.SetClock(func() time.Time { return later })
	if _, err := c.AdminIdentities(10); err != nil {
		t.Fatal(err)
	}
	if calls := countCalls(f, "GetChatAdministrators"); calls != 2 {
		t.Fatalf("expected 2 GetChatAdministrators calls after TTL expiry, got %d", calls)
	}
}

func TestAdminCacheErrorIsReturnedWithoutStaleData(t *testing.T) {
	f := fake.New()
	f.AdminsErr = errors.New("boom")
	c := telegram.NewAdminCache(f, time.Minute)

	ids, err := c.AdminIdentities(10)
	if err == nil {
		t.Fatal("expected admin lookup error")
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty slice on error, got %v", ids)
	}
}

// A failing refetch must keep serving the last good list for a bounded
// window instead of erroring: every caller reads an error as "cannot prove
// this sender is not an admin" and defers ALL moderation for the chat, so one
// transient Telegram error would otherwise switch off the blocklist and hard
// rules too.
func TestAdminCacheServesStaleWithinGraceOnRefetchFailure(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1, Username: "a"}}
	c := telegram.NewAdminCache(f, time.Minute)

	now := time.Now()
	c.SetClock(func() time.Time { return now })

	ids, err := c.AdminIdentities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 identity, got %d", len(ids))
	}

	// Advance past the TTL but stay inside the grace window, then fail.
	later := now.Add(90 * time.Second)
	c.SetClock(func() time.Time { return later })
	f.AdminsErr = errors.New("boom")

	ids, err = c.AdminIdentities(10)
	if err != nil {
		t.Fatalf("stale list inside the grace window must not error, got %v", err)
	}
	if len(ids) != 1 || ids[0].UserID != 1 {
		t.Fatalf("expected stale identity on refetch error, got %v", ids)
	}
}

// Past the grace window the cached list is too old to stand in for the real
// one, so the error surfaces and callers fall back to deferring.
func TestAdminCacheErrorsOnceStaleListAgesPastGrace(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Minute)

	now := time.Now()
	c.SetClock(func() time.Time { return now })
	if _, err := c.AdminIdentities(10); err != nil {
		t.Fatal(err)
	}

	f.AdminsErr = errors.New("boom")
	c.SetClock(func() time.Time { return now.Add(time.Hour) })

	ids, err := c.AdminIdentities(10)
	if err == nil {
		t.Fatal("expected the refetch error once the stale list aged out")
	}
	if len(ids) != 0 {
		t.Fatalf("expected no identities past the grace window, got %v", ids)
	}
}

// While a refetch keeps failing the cache must back off rather than issue a
// GetChatAdministrators job per inbound message: the dispatcher is a single
// goroutine behind a global rate limiter, and an outage would otherwise turn
// every message in the chat into another doomed request.
func TestAdminCacheBacksOffAfterFailedRefetch(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Minute)

	now := time.Now()
	c.SetClock(func() time.Time { return now })
	if _, err := c.AdminIdentities(10); err != nil {
		t.Fatal(err)
	}

	f.AdminsErr = errors.New("boom")
	c.SetClock(func() time.Time { return now.Add(90 * time.Second) })
	for i := 0; i < 5; i++ {
		if _, err := c.AdminIdentities(10); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if got := countCalls(f, "GetChatAdministrators"); got != 2 {
		t.Fatalf("GetChatAdministrators calls = %d, want 2 (initial + one failed refetch)", got)
	}

	// A TTL later the backoff has expired and exactly one retry is allowed,
	// however that retry turns out.
	c.SetClock(func() time.Time { return now.Add(3 * time.Minute) })
	for i := 0; i < 3; i++ {
		_, _ = c.AdminIdentities(10)
	}
	if got := countCalls(f, "GetChatAdministrators"); got != 3 {
		t.Fatalf("GetChatAdministrators calls = %d, want 3 (one retry after the backoff)", got)
	}
}

// An Invalidate issued while a fetch is in flight must win: otherwise the
// fetch lands afterwards and reinstates the pre-demotion list, stamped fresh,
// handing the demoted user admin immunity for a whole TTL — exactly what
// Invalidate exists to prevent.
func TestAdminCacheInvalidateDuringInFlightFetchIsNotOverwritten(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Hour)

	released := make(chan struct{})
	fetching := make(chan struct{})
	f.BeforeGetAdmins = func() {
		close(fetching)
		<-released
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The result predates the invalidation, so it must not be handed
		// back either: §4 defers rather than answer from a list we already
		// know is wrong.
		if ids, err := c.AdminIdentities(10); err == nil {
			t.Errorf("in-flight fetch must not return the pre-invalidation list, got %v", ids)
		}
	}()

	<-fetching
	// The demotion arrives while the fetch is blocked; the fetch then lands
	// carrying the pre-demotion list.
	c.Invalidate(10)
	close(released)
	<-done

	// Now the roster reflects the demotion. A cache that let the in-flight
	// result win would answer from it instead of refetching.
	f.BeforeGetAdmins = nil
	f.Admins = []telegram.Member{{UserID: 2}}

	ids, err := c.AdminIdentities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0].UserID != 2 {
		t.Fatalf("stale in-flight result was cached over Invalidate, got %v", ids)
	}
}

func TestAdminCacheInvalidateForcesRefetch(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Hour)

	if _, err := c.AdminIdentities(10); err != nil {
		t.Fatal(err)
	}
	c.Invalidate(10)
	f.Admins = []telegram.Member{{UserID: 2}}

	ids, err := c.AdminIdentities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0].UserID != 2 {
		t.Fatalf("expected invalidation to expose refreshed admins, got %v", ids)
	}
	if calls := countCalls(f, "GetChatAdministrators"); calls != 2 {
		t.Fatalf("expected 2 GetChatAdministrators calls after invalidation, got %d", calls)
	}
}

func TestAdminCacheUsesLifecycleContext(t *testing.T) {
	f := fake.New()
	c := telegram.NewAdminCache(f, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.SetContext(ctx)

	_, err := c.AdminIdentities(10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestAdminCacheRejectsEmptySuccessfulList(t *testing.T) {
	c := telegram.NewAdminCache(fake.New(), time.Hour)
	ids, err := c.AdminIdentities(10)
	if err == nil {
		t.Fatal("empty successful admin response must be treated as unavailable")
	}
	if len(ids) != 0 {
		t.Fatalf("unexpected identities: %v", ids)
	}
}

func countCalls(f *fake.Fake, name string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == name {
			n++
		}
	}
	return n
}

// A chat whose very first lookup fails has no list to fall back on, but it
// must still back off: otherwise every message during an outage queues
// another GetChatAdministrators on the single, globally rate-limited
// dispatcher.
func TestAdminCacheBacksOffWithNoCachedListAtAll(t *testing.T) {
	f := fake.New()
	f.AdminsErr = errors.New("boom")
	c := telegram.NewAdminCache(f, time.Minute)

	now := time.Now()
	c.SetClock(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if _, err := c.AdminIdentities(10); err == nil {
			t.Fatalf("call %d: expected an error with no cached list", i)
		}
	}
	if got := countCalls(f, "GetChatAdministrators"); got != 1 {
		t.Fatalf("GetChatAdministrators calls = %d, want 1 (the rest served by the negative cache)", got)
	}

	// Once the backoff expires, one more attempt goes out and succeeds.
	c.SetClock(func() time.Time { return now.Add(2 * time.Minute) })
	f.AdminsErr = nil
	f.Admins = []telegram.Member{{UserID: 7}}

	ids, err := c.AdminIdentities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0].UserID != 7 {
		t.Fatalf("expected the recovered list, got %v", ids)
	}
	if got := countCalls(f, "GetChatAdministrators"); got != 2 {
		t.Fatalf("GetChatAdministrators calls = %d, want 2", got)
	}
}
