package telegram_test

import (
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

	ids := c.AdminIdentities(10)
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
	c.AdminIdentities(10)
	if calls := countCalls(f, "GetChatAdministrators"); calls != 1 {
		t.Fatalf("expected still 1 GetChatAdministrators call (cached), got %d", calls)
	}

	// Advance past TTL: refetch.
	later := now.Add(2 * time.Minute)
	c.SetClock(func() time.Time { return later })
	c.AdminIdentities(10)
	if calls := countCalls(f, "GetChatAdministrators"); calls != 2 {
		t.Fatalf("expected 2 GetChatAdministrators calls after TTL expiry, got %d", calls)
	}
}

func TestAdminCacheErrorReturnsEmptyNoStale(t *testing.T) {
	f := fake.New()
	f.AdminsErr = errors.New("boom")
	c := telegram.NewAdminCache(f, time.Minute)

	ids := c.AdminIdentities(10)
	if len(ids) != 0 {
		t.Fatalf("expected empty slice on error, got %v", ids)
	}
}

func TestAdminCacheErrorReturnsStaleOnRefetchFailure(t *testing.T) {
	f := fake.New()
	f.Admins = []telegram.Member{{UserID: 1, Username: "a"}}
	c := telegram.NewAdminCache(f, time.Minute)

	now := time.Now()
	c.SetClock(func() time.Time { return now })

	ids := c.AdminIdentities(10)
	if len(ids) != 1 {
		t.Fatalf("expected 1 identity, got %d", len(ids))
	}

	// Advance past TTL and make the port fail; expect stale data back.
	later := now.Add(2 * time.Minute)
	c.SetClock(func() time.Time { return later })
	f.AdminsErr = errors.New("boom")

	ids = c.AdminIdentities(10)
	if len(ids) != 1 || ids[0].UserID != 1 {
		t.Fatalf("expected stale identity on refetch error, got %v", ids)
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
