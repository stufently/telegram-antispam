package ops

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthzAndMetrics(t *testing.T) {
	reg := NewRegistry()
	srv := httptest.NewServer(handler(reg))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /healthz body: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("/healthz body = %q, want %q", string(body), "ok")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("/healthz Content-Type = %q", ct)
	}

	reg.IncCounter("x", 1)

	mresp, err := srv.Client().Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer mresp.Body.Close()

	if mresp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", mresp.StatusCode)
	}
	mbody, err := io.ReadAll(mresp.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	if !strings.Contains(string(mbody), "x 1") {
		t.Fatalf("/metrics body = %q, want it to contain %q", string(mbody), "x 1")
	}
	if ct := mresp.Header.Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("/metrics Content-Type = %q", ct)
	}
}

func TestServerRunStopsOnCancel(t *testing.T) {
	reg := NewRegistry()
	s := NewServer("127.0.0.1:0", reg)

	ctx, cancel := context.WithCancel(context.Background())

	errc := make(chan error, 1)
	go func() {
		errc <- s.Run(ctx)
	}()

	// Give ListenAndServe a brief moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Run() returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return within 2s after ctx cancel")
	}
}
