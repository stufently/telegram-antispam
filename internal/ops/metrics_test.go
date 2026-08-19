package ops

import (
	"strings"
	"sync"
	"testing"
)

func TestRegistryExposition(t *testing.T) {
	r := NewRegistry()
	r.IncCounter("updates_total", 1)
	r.IncCounter("updates_total", 2)
	r.IncCounter("incidents_total", 1, "action", "ban")
	r.SetGauge("blocklist_size", 4860000)

	var b strings.Builder
	r.Write(&b)
	out := b.String()

	for _, want := range []string{
		"# TYPE updates_total counter",
		"updates_total 3",
		`incidents_total{action="ban"} 1`,
		"# TYPE blocklist_size gauge",
		"blocklist_size 4.86e+06",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition missing %q\n---\n%s", want, out)
		}
	}
}

func TestIncCounter_OddLabelsIgnoresTrailing(t *testing.T) {
	r := NewRegistry()
	// odd label count: trailing unpaired label "action" is ignored,
	// so this behaves as an unlabeled increment.
	r.IncCounter("odd_total", 1, "action")

	var b strings.Builder
	r.Write(&b)
	out := b.String()

	if !strings.Contains(out, "odd_total 1") {
		t.Errorf("expected odd_total 1 (unlabeled) in output\n---\n%s", out)
	}
	if strings.Contains(out, "odd_total{") {
		t.Errorf("did not expect a labeled sample for odd_total\n---\n%s", out)
	}
}

func TestSetGauge_OverwritesValue(t *testing.T) {
	r := NewRegistry()
	r.SetGauge("queue_depth", 5)
	r.SetGauge("queue_depth", 9)

	var b strings.Builder
	r.Write(&b)
	out := b.String()

	if !strings.Contains(out, "queue_depth 9") {
		t.Errorf("expected latest gauge value 9\n---\n%s", out)
	}
	if strings.Contains(out, "queue_depth 5") {
		t.Errorf("did not expect stale gauge value 5\n---\n%s", out)
	}
}

func TestLabelValueEscaping(t *testing.T) {
	r := NewRegistry()
	r.IncCounter("errors_total", 1, "msg", `bad "quote" and \backslash`)

	var b strings.Builder
	r.Write(&b)
	out := b.String()

	want := `errors_total{msg="bad \"quote\" and \\backslash"} 1`
	if !strings.Contains(out, want) {
		t.Errorf("exposition missing escaped label %q\n---\n%s", want, out)
	}
}

func TestLabelValueEscapesNewline(t *testing.T) {
	r := NewRegistry()
	r.IncCounter("errors_total", 1, "msg", "line1\nline2")
	var b strings.Builder
	r.Write(&b)
	out := b.String()
	// A raw newline in a label value would break the Prometheus text format
	// (one sample per line); it must be escaped to the two-char sequence \n.
	if strings.Contains(out, "line1\nline2") {
		t.Errorf("raw newline leaked into exposition:\n%s", out)
	}
	if !strings.Contains(out, `msg="line1\nline2"`) {
		t.Errorf("newline not escaped to \\n:\n%s", out)
	}
}

func TestMultipleLabelSetsSortedKeys(t *testing.T) {
	r := NewRegistry()
	r.IncCounter("requests_total", 1, "b", "2", "a", "1")

	var b strings.Builder
	r.Write(&b)
	out := b.String()

	want := `requests_total{a="1",b="2"} 1`
	if !strings.Contains(out, want) {
		t.Errorf("expected sorted label keys %q\n---\n%s", want, out)
	}
}

func TestConcurrentIncCounter(t *testing.T) {
	r := NewRegistry()
	const goroutines = 50
	const perGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				r.IncCounter("concurrent_total", 1)
			}
		}()
	}
	wg.Wait()

	var b strings.Builder
	r.Write(&b)
	out := b.String()

	want := "concurrent_total 5000"
	if !strings.Contains(out, want) {
		t.Errorf("expected %q after concurrent increments\n---\n%s", want, out)
	}
}
