package telegram_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stufently/telegram-antispam/internal/detect"
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

// A failing refetch returns the last good list AND the error: the list is
// what still proves a sender was an admin, the error is what stops a caller
// treating absence from it as proof of the opposite.
func TestAdminCacheReturnsStaleListWithErrorWithinGrace(t *testing.T) {
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
	if err == nil {
		t.Fatal("a stale list must still carry the refetch error")
	}
	if len(ids) != 1 || ids[0].UserID != 1 {
		t.Fatalf("expected stale identity alongside the error, got %v", ids)
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
		if _, err := c.AdminIdentities(10); err == nil {
			t.Fatalf("call %d: expected the refetch error", i)
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

// Concurrent misses for one chat must share a single request. Without
// single-flighting, a burst all misses at once, every one issues its own
// GetChatAdministrators on the single rate-limited dispatcher, and whichever
// finishes last installs its result — which may be the older of the two.
func TestAdminCacheSingleFlightsConcurrentMisses(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Hour)

	// Hold the first port call open so every other caller is guaranteed to
	// arrive while it is still in flight.
	release := make(chan struct{})
	fetching := make(chan struct{})
	var once sync.Once
	f.BeforeGetAdmins = func() {
		once.Do(func() { close(fetching) })
		<-release
	}

	const callers = 8
	var wg sync.WaitGroup
	ids := make([]int64, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := c.AdminIdentities(10)
			errs[i] = err
			if len(got) > 0 {
				ids[i] = got[0].UserID
			}
		}(i)
	}

	// One caller is inside the port call; the rest only need to take the
	// cache mutex, see the in-flight entry, and park on it.
	<-fetching
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := countCalls(f, "GetChatAdministrators"); got != 1 {
		t.Fatalf("GetChatAdministrators calls = %d, want 1 for %d concurrent misses", got, callers)
	}
	for i := range ids {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if ids[i] != 1 {
			t.Fatalf("caller %d got user %d, want the shared result", i, ids[i])
		}
	}
}

// A fetch that never returns normally must not strand its waiters on the
// done channel, and must not hand them a nil error either — that would read
// as "resolved, no admins" and let every sender past the §4 immunity gate.
func TestAdminCacheAbandonedFetchReleasesWaitersWithError(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Hour)

	fetching := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.BeforeGetAdmins = func() {
		once.Do(func() { close(fetching) })
		<-release
		panic("telegram client blew up")
	}

	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		defer func() { _ = recover() }()
		_, _ = c.AdminIdentities(10)
	}()
	<-fetching

	waiterDone := make(chan struct{})
	var waiterIDs []detect.AdminIdentity
	var waiterErr error
	go func() {
		defer close(waiterDone)
		waiterIDs, waiterErr = c.AdminIdentities(10)
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)

	<-leaderDone
	select {
	case <-waiterDone:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter was left parked on an abandoned fetch")
	}

	if waiterErr == nil {
		t.Fatalf("waiter got a nil error (ids=%v); an abandoned fetch must fail closed", waiterIDs)
	}
	if len(waiterIDs) != 0 {
		t.Fatalf("waiter got identities %v from an abandoned fetch", waiterIDs)
	}
}

// Waiters on a shared fetch get exactly what the leader got, including the
// stale-list-with-error pairing. A waiter that saw only the error would
// wrongly defer an administrator the stale list still vouches for.
func TestAdminCacheSharedFailureGivesWaitersStaleListAndError(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Minute)

	now := time.Now()
	c.SetClock(func() time.Time { return now })
	if _, err := c.AdminIdentities(10); err != nil {
		t.Fatal(err)
	}

	// Past the TTL, inside the grace window, with the port now failing.
	c.SetClock(func() time.Time { return now.Add(90 * time.Second) })
	f.AdminsErr = errors.New("boom")

	fetching := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.BeforeGetAdmins = func() {
		once.Do(func() { close(fetching) })
		<-release
	}

	const callers = 4
	var wg sync.WaitGroup
	ids := make([][]detect.AdminIdentity, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = c.AdminIdentities(10)
		}(i)
	}
	<-fetching
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := countCalls(f, "GetChatAdministrators"); got != 2 {
		t.Fatalf("GetChatAdministrators calls = %d, want 2 (initial + one shared refetch)", got)
	}
	for i := range ids {
		if errs[i] == nil {
			t.Fatalf("caller %d got a nil error; the stale list must carry the failure", i)
		}
		if len(ids[i]) != 1 || ids[i][0].UserID != 1 {
			t.Fatalf("caller %d got %v, want the shared stale list", i, ids[i])
		}
	}
}

// An Invalidate that lands while a shared fetch is in flight must reach the
// waiters too: publishing the pre-invalidation list to them with a nil error
// is the one combination the contract forbids.
func TestAdminCacheInvalidateDuringSharedFetchFailsEveryCaller(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1}}
	c := telegram.NewAdminCache(f, time.Hour)

	fetching := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.BeforeGetAdmins = func() {
		once.Do(func() { close(fetching) })
		<-release
	}

	const callers = 4
	var wg sync.WaitGroup
	ids := make([][]detect.AdminIdentity, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = c.AdminIdentities(10)
		}(i)
	}
	<-fetching
	time.Sleep(50 * time.Millisecond)
	// The demotion lands while every caller is committed to this fetch.
	c.Invalidate(10)
	close(release)
	wg.Wait()

	for i := range ids {
		if errs[i] == nil {
			t.Fatalf("caller %d got a nil error alongside %v; an invalidated list must never resolve", i, ids[i])
		}
		if len(ids[i]) != 0 {
			t.Fatalf("caller %d got identities %v from an invalidated fetch", i, ids[i])
		}
	}
}
