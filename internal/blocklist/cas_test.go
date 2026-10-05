package blocklist

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type casProbe struct {
	mu       sync.Mutex
	requests int
	counts   map[string]int
}

func newCASTest(t *testing.T, cfg CASConfig, handler http.HandlerFunc) (*CASChecker, *casProbe) {
	t.Helper()
	p := &casProbe{counts: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.requests++
		p.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.URL = srv.URL + "/check?discard=me&api_key=unused"
	c := NewCASChecker(cfg, srv.Client())
	c.Now = func() time.Time { return time.Unix(1700000000, 0) }
	c.Count = func(result string) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.counts[result]++
	}
	return c, p
}

func (p *casProbe) assert(t *testing.T, requests int, counts map[string]int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.requests != requests || !maps.Equal(p.counts, counts) {
		t.Fatalf("requests=%d counts=%v; want requests=%d counts=%v", p.requests, p.counts, requests, counts)
	}
}

func casReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func TestCASCheckListed(t *testing.T) {
	c, p := newCASTest(t, CASConfig{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/check" || r.URL.Query().Get("user_id") != "94398985" || len(r.URL.Query()) != 1 || len(r.URL.Query()["user_id"]) != 1 {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.UserAgent() != "telegram-antispam-blocklist/1.0 (+https://github.com/stufently/telegram-antispam)" {
			t.Errorf("unexpected User-Agent: %q", r.UserAgent())
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"reasons":[1],"offenses":1,"messages":["ignored"],"time_added":"ignored"}}`)
	})
	if !c.Listed(94398985) {
		t.Fatal("listed user returned false")
	}
	p.assert(t, 1, map[string]int{"listed": 1})
}

func TestCASCheckClean(t *testing.T) {
	for _, body := range []string{`{"ok":false,"description":"Record not found."}`, `{"ok":false,"error":"Record not found."}`} {
		t.Run(body, func(t *testing.T) {
			c, p := newCASTest(t, CASConfig{}, casReply(body))
			if c.Listed(1) {
				t.Fatal("clean user returned true")
			}
			p.assert(t, 1, map[string]int{"clean": 1})
		})
	}
}

func TestCASCheckCachesWithinTTL(t *testing.T) {
	for _, tc := range []struct {
		name   string
		listed bool
		ttl    time.Duration
		result string
	}{
		{"positive", true, 24 * time.Hour, "listed"}, {"negative", false, 6 * time.Hour, "clean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p := newCASTest(t, CASConfig{PositiveTTL: 24 * time.Hour, NegativeTTL: 6 * time.Hour}, casReply(fmt.Sprintf(`{"ok":%t}`, tc.listed)))
			now := time.Unix(1700000000, 0)
			c.Now = func() time.Time { return now }
			if c.Listed(1) != tc.listed {
				t.Fatal("wrong initial result")
			}
			now = now.Add(tc.ttl - time.Nanosecond)
			if c.Listed(1) != tc.listed {
				t.Fatal("wrong cached result")
			}
			p.assert(t, 1, map[string]int{tc.result: 1})
			now = now.Add(time.Nanosecond)
			if c.Listed(1) != tc.listed {
				t.Fatal("wrong result at expiry")
			}
			p.assert(t, 2, map[string]int{tc.result: 2})
			now = now.Add(time.Nanosecond)
			if c.Listed(1) != tc.listed {
				t.Fatal("wrong refreshed result")
			}
			p.assert(t, 2, map[string]int{tc.result: 2})
		})
	}
}

func TestCASCheckErrorsFailOpenUncached(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"http500", 500, `{"ok":true}`}, {"invalid", 200, `{"ok":`}, {"missing", 200, `{"result":{}}`},
		{"null", 200, `{"ok":null}`}, {"wrong_type", 200, `{"ok":"true"}`},
		{"trailing", 200, `{"ok":true} garbage`}, {"two_objects", 200, `{"ok":true}{"ok":false}`},
		{"oversize", 200, `{"ok":true,"padding":"` + strings.Repeat("x", 1<<20) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p := newCASTest(t, CASConfig{}, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			for range 2 {
				if c.Listed(1) {
					t.Fatal("error returned listed")
				}
			}
			p.assert(t, 2, map[string]int{"error": 2})
		})
	}
}

func TestCASCheckTimeout(t *testing.T) {
	for _, bodyStarted := range []bool{false, true} {
		t.Run(fmt.Sprintf("body_started_%t", bodyStarted), func(t *testing.T) {
			c, p := newCASTest(t, CASConfig{Timeout: 50 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
				if bodyStarted {
					_, _ = io.WriteString(w, `{"ok":`)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second):
					_, _ = io.WriteString(w, `{"ok":true}`)
				}
			})
			start := time.Now()
			if c.Listed(1) {
				t.Fatal("timeout returned listed")
			}
			if elapsed := time.Since(start); elapsed > 1050*time.Millisecond {
				t.Fatalf("timeout took %v", elapsed)
			}
			p.assert(t, 1, map[string]int{"error": 1})
			if c.Listed(1) {
				t.Fatal("timeout retry returned listed")
			}
			p.assert(t, 2, map[string]int{"error": 2})
		})
	}
}

func TestCASCheckRateLimited(t *testing.T) {
	c, p := newCASTest(t, CASConfig{RatePerSec: 1, Burst: 2}, casReply(`{"ok":true}`))
	now := time.Unix(1700000000, 0)
	c.Now = func() time.Time { return now }
	if !c.Listed(1) || !c.Listed(2) {
		t.Fatal("initial burst rejected")
	}
	if c.Listed(3) {
		t.Fatal("exhausted burst allowed request")
	}
	if !c.Listed(1) {
		t.Fatal("rate limit hid cached hit")
	}
	p.assert(t, 2, map[string]int{"listed": 2, "rate_limited": 1})
	now = now.Add(time.Second)
	if !c.Listed(3) {
		t.Fatal("refilled limiter rejected request")
	}
	p.assert(t, 3, map[string]int{"listed": 3, "rate_limited": 1})
}

func TestCASCheckBreaker(t *testing.T) {
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })
	var responseMu sync.Mutex
	fail := false
	c, p := newCASTest(t, CASConfig{Burst: 100}, func(w http.ResponseWriter, _ *http.Request) {
		responseMu.Lock()
		defer responseMu.Unlock()
		if fail {
			w.WriteHeader(500)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	setFailure := func(value bool) { responseMu.Lock(); fail = value; responseMu.Unlock() }
	now := time.Unix(1700000000, 0)
	c.Now = func() time.Time { return now }
	if !c.Listed(99) {
		t.Fatal("seed cache failed")
	}
	setFailure(true)
	for range 3 {
		if c.Listed(1) {
			t.Fatal("error returned listed")
		}
	}
	if c.Listed(1) {
		t.Fatal("breaker returned listed")
	}
	if !c.Listed(99) {
		t.Fatal("breaker hid cached hit")
	}
	p.assert(t, 4, map[string]int{"listed": 1, "error": 3, "breaker_open": 1})
	if strings.Count(logs.String(), "\n") != 1 || !strings.Contains(logs.String(), "500") {
		t.Fatalf("opening log = %q", logs.String())
	}
	now = now.Add(60*time.Second - time.Nanosecond)
	if c.Listed(1) {
		t.Fatal("breaker returned listed before expiry")
	}
	p.assert(t, 4, map[string]int{"listed": 1, "error": 3, "breaker_open": 2})
	now = now.Add(time.Nanosecond)
	if c.Listed(1) || c.Listed(1) {
		t.Fatal("failed probe returned listed")
	}
	p.assert(t, 5, map[string]int{"listed": 1, "error": 4, "breaker_open": 3})
	if strings.Count(logs.String(), "\n") != 2 {
		t.Fatalf("reopen log = %q", logs.String())
	}
	now = now.Add(60 * time.Second)
	setFailure(false)
	if !c.Listed(1) {
		t.Fatal("recovery probe failed")
	}
	p.assert(t, 6, map[string]int{"listed": 2, "error": 4, "breaker_open": 3})
	if strings.Count(logs.String(), "\n") != 3 {
		t.Fatalf("recovery log = %q", logs.String())
	}
	setFailure(true)
	for range 2 {
		if c.Listed(2) {
			t.Fatal("error returned listed")
		}
	}
	setFailure(false)
	if !c.Listed(2) || !c.Listed(3) {
		t.Fatal("successful probe failed to reset errors")
	}
	p.assert(t, 10, map[string]int{"listed": 4, "error": 6, "breaker_open": 3})
	if strings.Count(logs.String(), "\n") != 3 {
		t.Fatalf("unexpected extra logs = %q", logs.String())
	}
}

func TestCASCheckZeroUserNoRequest(t *testing.T) {
	c, p := newCASTest(t, CASConfig{}, casReply(`{"ok":true}`))
	for _, id := range []int64{0, -1, -94398985} {
		if c.Listed(id) {
			t.Fatalf("Listed(%d)=true", id)
		}
	}
	p.assert(t, 0, nil)
}

func TestCASCheckCacheBounded(t *testing.T) {
	c, p := newCASTest(t, CASConfig{MaxEntries: 2, PositiveTTL: time.Hour}, casReply(`{"ok":true}`))
	now := time.Unix(1700000000, 0)
	c.Now = func() time.Time { return now }
	for _, id := range []int64{1, 2, 3, 3, 1, 2} {
		if !c.Listed(id) {
			t.Fatalf("Listed(%d)=false", id)
		}
	}
	p.assert(t, 4, map[string]int{"listed": 4})
	now = now.Add(time.Hour)
	if !c.Listed(3) || !c.Listed(3) {
		t.Fatal("expired entries prevented new cache entry")
	}
	p.assert(t, 5, map[string]int{"listed": 5})
}

func TestCASCheckConcurrent(t *testing.T) {
	c, p := newCASTest(t, CASConfig{Burst: 1000, Timeout: 5 * time.Second}, func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
		if err != nil {
			t.Error(err)
		}
		_, _ = fmt.Fprintf(w, `{"ok":%t}`, id%2 == 0)
	})
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			for _, id := range []int64{int64(i + 1), 100, 101} {
				if got := c.Listed(id); got != (id%2 == 0) {
					t.Errorf("Listed(%d)=%t", id, got)
				}
			}
		})
	}
	wg.Wait()
	p.mu.Lock()
	requests := p.requests
	listed, clean := p.counts["listed"], p.counts["clean"]
	p.mu.Unlock()
	if requests < 52 || requests > 150 || listed < 26 || clean < 26 {
		t.Fatalf("requests=%d listed=%d clean=%d", requests, listed, clean)
	}
	p.assert(t, listed+clean, map[string]int{"listed": listed, "clean": clean})
	for _, id := range []int64{1, 2, 100, 101} {
		if c.Listed(id) != (id%2 == 0) {
			t.Fatal("wrong cached concurrent result")
		}
	}
	p.assert(t, requests, map[string]int{"listed": listed, "clean": clean})
}

type casTestSource func(int64) bool

func (f casTestSource) Listed(id int64) bool { return f(id) }

func TestAnyOfShortCircuits(t *testing.T) {
	for _, tc := range []struct {
		first, second bool
		calls         int
		want          bool
	}{
		{true, true, 1, true}, {false, true, 2, true}, {false, false, 2, false},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			calls := 0
			source := func(result bool) Source {
				return casTestSource(func(id int64) bool {
					if id != 7 {
						t.Errorf("id=%d want 7", id)
					}
					calls++
					return result
				})
			}
			combined := AnyOf(nil, source(tc.first), nil, source(tc.second))
			if combined.Listed(7) != tc.want || calls != tc.calls {
				t.Fatalf("calls=%d want %d", calls, tc.calls)
			}
		})
	}
	if AnyOf(nil, nil).Listed(7) || AnyOf().Listed(7) {
		t.Fatal("empty sources returned true")
	}
}

func TestCASCheckerDefaults(t *testing.T) {
	for _, cfg := range []CASConfig{{}, {PositiveTTL: -1, NegativeTTL: -1, Timeout: -1, RatePerSec: -1, Burst: -1, MaxEntries: -1}} {
		c := NewCASChecker(cfg, nil)
		want := CASConfig{URL: "https://api.cas.chat/check", PositiveTTL: 24 * time.Hour, NegativeTTL: 6 * time.Hour, Timeout: 2 * time.Second, RatePerSec: 10, Burst: 20, MaxEntries: 100000}
		if c.cfg != want {
			t.Fatalf("defaults=%+v want %+v", c.cfg, want)
		}
		if c.client == nil || c.client.Timeout != 2*time.Second {
			t.Fatal("nil client did not get default timeout")
		}
	}
}
