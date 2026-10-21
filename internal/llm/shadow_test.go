package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type shadowFake struct {
	name string
	call func(context.Context, string, string) (bool, error)
}

func (p shadowFake) Name() string { return p.name }
func (p shadowFake) Classify(ctx context.Context, text, prompt string) (bool, error) {
	return p.call(ctx, text, prompt)
}

type scoreFake struct {
	value, cutoff float64
	classified    atomic.Int32
}

func (p *scoreFake) Name() string { return "scorer" }
func (p *scoreFake) Classify(context.Context, string, string) (bool, error) {
	p.classified.Add(1)
	return false, nil
}
func (p *scoreFake) Score(context.Context, string, string) (float64, error) { return p.value, nil }
func (p *scoreFake) Cutoff() float64                                        { return p.cutoff }
func shadowWait(t *testing.T, ch <-chan []Vote) []Vote {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("report timeout")
		return nil
	}
}

func TestAdjudicateEachPerProviderPrompt(t *testing.T) {
	a, b := &promptCapture{}, &promptCapture{}
	j := Judge{Providers: []Provider{a, b}, Policy: PolicyAny}
	j.AdjudicateEach(context.Background(), "text", []string{"one", "two"})
	if a.got != "one" || b.got != "two" {
		t.Fatalf("%q %q", a.got, b.got)
	}
	j.Adjudicate(context.Background(), "text", "shared")
	if a.got != "shared" || b.got != "shared" {
		t.Fatalf("%q %q", a.got, b.got)
	}
	j.AdjudicateEach(context.Background(), "text", []string{"wrong length"})
	if a.got != "" || b.got != "" {
		t.Fatalf("%q %q", a.got, b.got)
	}
}

func TestAdjudicateVotes(t *testing.T) {
	boom := errors.New("boom")
	j := Judge{Providers: []Provider{stub{name: "spam", spam: true}, stub{name: "bad", err: boom}, stub{name: "ham"}}, Policy: PolicyAny}
	out := j.AdjudicateEach(context.Background(), "x", nil)
	old := j.Adjudicate(context.Background(), "x", "")
	if out.Spam != old.Spam || out.Failed != old.Failed || out.Total != old.Total || !out.Spam || out.Failed != 1 || out.Total != 3 {
		t.Fatalf("out=%+v old=%+v", out, old)
	}
	if len(out.Votes) != 3 {
		t.Fatal(out)
	}
	for i, name := range []string{"spam", "bad", "ham"} {
		if out.Votes[i].Provider != name || out.Votes[i].OK != (i != 1) || out.Votes[i].Spam != (i == 0) || out.Votes[i].Score != nil {
			t.Fatal(out.Votes)
		}
	}
	if !errors.Is(out.Votes[1].Err, boom) || out.Votes[0].Err != nil || out.Votes[2].Err != nil {
		t.Fatal(out.Votes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out = (Judge{Providers: []Provider{slowStub{}}, Policy: PolicyAny}).AdjudicateEach(ctx, "x", nil)
	if out.Votes[0].OK || !errors.Is(out.Votes[0].Err, context.Canceled) || out.Failed != 1 || out.Spam {
		t.Fatal(out)
	}
}

func TestShadowReportsVotes(t *testing.T) {
	for _, value := range []float64{.42, .41} {
		p := &scoreFake{value: value, cutoff: .42}
		capture := &promptCapture{}
		s := Shadow{Providers: []Provider{p, capture}, Timeout: time.Second}
		ch := make(chan []Vote, 2)
		var calls atomic.Int32
		if !s.Observe(context.Background(), "x", []string{"score prompt", "plain prompt"}, func(v []Vote) { calls.Add(1); ch <- v }) {
			t.Fatal("dropped")
		}
		v := shadowWait(t, ch)
		if len(v) != 2 || v[0].Provider != "scorer" || v[1].Provider != "capture" || !v[0].OK || !v[1].OK || v[0].Score == nil || *v[0].Score != value || v[0].Spam != (value >= .42) || v[1].Score != nil || p.classified.Load() != 0 || capture.got != "plain prompt" || calls.Load() != 1 {
			t.Fatalf("votes=%+v", v)
		}
	}
}

func TestShadowDropsWhenFull(t *testing.T) {
	entered, release, reporting, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := shadowFake{name: "blocking", call: func(context.Context, string, string) (bool, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return false, nil
	}}
	s := Shadow{Providers: []Provider{p}, Timeout: time.Second, Max: 1}
	if !s.Observe(context.Background(), "x", nil, func([]Vote) { close(reporting); <-finish }) {
		t.Fatal("first dropped")
	}
	<-entered
	report := make(chan []Vote, 2)
	if s.Observe(context.Background(), "x", nil, func(v []Vote) { report <- v }) {
		t.Fatal("accepted while full")
	}
	if calls.Load() != 1 {
		t.Fatal("called dropped provider")
	}
	close(release)
	<-reporting
	if s.Observe(context.Background(), "x", nil, func(v []Vote) { report <- v }) {
		t.Fatal("slot released before report returned")
	}
	close(finish)
	deadline := time.Now().Add(time.Second)
	for !s.Observe(context.Background(), "x", nil, func(v []Vote) { report <- v }) {
		if time.Now().After(deadline) {
			t.Fatal("slot not released")
		}
		time.Sleep(time.Millisecond)
	}
	shadowWait(t, report)
	if calls.Load() != 2 || len(report) != 0 {
		t.Fatal("wrong call/report count")
	}
}

func TestShadowTimeout(t *testing.T) {
	s := Shadow{Providers: []Provider{slowStub{}}, Timeout: 50 * time.Millisecond}
	ch := make(chan []Vote, 1)
	start := time.Now()
	s.Observe(context.Background(), "x", nil, func(v []Vote) { ch <- v })
	v := shadowWait(t, ch)
	if v[0].OK || !errors.Is(v[0].Err, context.DeadlineExceeded) || time.Since(start) > 1050*time.Millisecond {
		t.Fatal(v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s2 := Shadow{Providers: []Provider{slowStub{}}, Timeout: time.Second}
	s2.Observe(ctx, "x", nil, func(v []Vote) { ch <- v })
	if v = shadowWait(t, ch); v[0].OK || !errors.Is(v[0].Err, context.Canceled) {
		t.Fatal(v)
	}
}

func TestJEVScoreAndCutoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"answers":{"spam":{"type":"noul","noul":0.42}}}`)
	}))
	defer srv.Close()
	for _, cutoff := range []float64{.42, .43} {
		p := JEV{BaseURL: srv.URL, Client: srv.Client(), Threshold: cutoff, MaxSymbols: 6000}
		score, err := p.Score(context.Background(), "x", "")
		if err != nil || score != .42 || p.Cutoff() != cutoff {
			t.Fatalf("score=%v err=%v", score, err)
		}
		spam, err := p.Classify(context.Background(), "x", "")
		if err != nil || spam != (.42 >= cutoff) {
			t.Fatalf("spam=%v err=%v", spam, err)
		}
	}
}

// Concurrent admissions must share one bound, including the default Max.
func TestShadowConcurrentAdmission(t *testing.T) {
	for _, max := range []int{-1, 0, 3} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			bound := max
			if bound <= 0 {
				bound = 1
			}
			gate := make(chan struct{})
			var calls atomic.Int32
			p := shadowFake{name: "blocked", call: func(ctx context.Context, _, _ string) (bool, error) {
				calls.Add(1)
				select {
				case <-gate:
					return false, nil
				case <-ctx.Done():
					return false, ctx.Err()
				}
			}}
			s := Shadow{Providers: []Provider{p}, Max: max, Timeout: 10 * time.Second}
			reports := make(chan []Vote, 32)
			var accepted atomic.Int32
			var wg sync.WaitGroup
			for range 32 {
				wg.Go(func() {
					if s.Observe(context.Background(), "x", nil, func(v []Vote) { reports <- v }) {
						accepted.Add(1)
					}
				})
			}
			wg.Wait()
			close(gate)
			if int(accepted.Load()) != bound {
				t.Fatalf("accepted=%d want %d", accepted.Load(), bound)
			}
			for range bound {
				shadowWait(t, reports)
			}
			if int(calls.Load()) != bound {
				t.Fatalf("calls=%d want %d", calls.Load(), bound)
			}
		})
	}
}
