package ops

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLivezReflectsTheLastSuccessfulTelegramCall: the old /healthz was a
// static 200, so a revoked token or a wedged poller looked perfectly
// healthy. /livez must say otherwise once the probe has been failing for
// longer than the window.
func TestLivezReflectsTheLastSuccessfulTelegramCall(t *testing.T) {
	start := time.Now()
	h := NewHealth(15*time.Minute, start)
	srv := httptest.NewServer(handler(NewRegistry(), h))
	defer srv.Close()

	get := func(path string) int {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	if code := get("/livez"); code != http.StatusOK {
		t.Fatalf("fresh start: /livez = %d, want 200", code)
	}

	// Rewind the last success beyond the window.
	h.Beat(start.Add(-16 * time.Minute))
	if code := get("/livez"); code != http.StatusServiceUnavailable {
		t.Fatalf("stale probe: /livez = %d, want 503", code)
	}
	// Readiness must NOT follow: dropping the pod out of the Service would
	// also stop /metrics being scraped, blinding monitoring exactly when
	// something is wrong.
	if code := get("/healthz"); code != http.StatusOK {
		t.Fatalf("stale probe: /healthz = %d, want 200 (readiness is about the process)", code)
	}

	h.Beat(time.Now())
	if code := get("/livez"); code != http.StatusOK {
		t.Fatalf("after a successful probe: /livez = %d, want 200", code)
	}
}

// TestLivezWithoutAProbeStaysUp: a build that never wires the probe must not
// restart-loop on a 503 it can never clear.
func TestLivezWithoutAProbeStaysUp(t *testing.T) {
	srv := httptest.NewServer(handler(NewRegistry(), nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/livez")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/livez without a probe = %d, want 200", resp.StatusCode)
	}
}
