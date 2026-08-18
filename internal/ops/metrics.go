// Package ops provides a minimal, dependency-free metrics registry that
// exposes counters and gauges in Prometheus text-exposition format.
//
// It intentionally has no external dependencies (stdlib only) to keep the
// project's operational footprint small. It is safe for concurrent use.
package ops

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// metricType distinguishes counters from gauges in the exposition output.
type metricType int

const (
	typeCounter metricType = iota
	typeGauge
)

func (t metricType) String() string {
	if t == typeGauge {
		return "gauge"
	}
	return "counter"
}

// metric holds all label-sets recorded for a single metric name.
type metric struct {
	typ metricType
	// values is keyed by the canonical, sorted label-set string (e.g.
	// `action="ban"`, or "" for no labels), mapping to the current value.
	values map[string]float64
}

// Registry is a thread-safe collection of counters and gauges.
//
// Zero value is not usable; construct with NewRegistry.
type Registry struct {
	mu      sync.Mutex
	metrics map[string]*metric
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		metrics: make(map[string]*metric),
	}
}

// IncCounter increments the counter identified by name by delta, creating it
// if it does not yet exist. labels must be an even-length list of
// alternating keys and values (k1, v1, k2, v2, ...). If len(labels) is odd,
// the trailing unpaired label is ignored (not an error) and the metric is
// recorded as if it had one fewer (unpaired) label.
func (r *Registry) IncCounter(name string, delta float64, labels ...string) {
	key := labelSetKey(labels)

	r.mu.Lock()
	defer r.mu.Unlock()

	m := r.metrics[name]
	if m == nil {
		m = &metric{typ: typeCounter, values: make(map[string]float64)}
		r.metrics[name] = m
	}
	m.values[key] += delta
}

// SetGauge sets the gauge identified by name to value, creating it if it
// does not yet exist. Label rules are identical to IncCounter.
func (r *Registry) SetGauge(name string, value float64, labels ...string) {
	key := labelSetKey(labels)

	r.mu.Lock()
	defer r.mu.Unlock()

	m := r.metrics[name]
	if m == nil {
		m = &metric{typ: typeGauge, values: make(map[string]float64)}
		r.metrics[name] = m
	}
	m.values[key] = value
}

// Write renders all recorded metrics to w in Prometheus text-exposition
// format. For each metric name (sorted), it emits one "# TYPE" line followed
// by one sample line per label-set (label keys sorted for deterministic
// output).
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	names := make([]string, 0, len(r.metrics))
	for name := range r.metrics {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		m := r.metrics[name]
		fmt.Fprintf(w, "# TYPE %s %s\n", name, m.typ)

		keys := make([]string, 0, len(m.values))
		for k := range m.values {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			v := m.values[k]
			if k == "" {
				fmt.Fprintf(w, "%s %s\n", name, formatValue(v))
			} else {
				fmt.Fprintf(w, "%s{%s} %s\n", name, k, formatValue(v))
			}
		}
	}
}

// labelSetKey builds the canonical, sorted `k="v",...` string for a labels
// list. If len(labels) is odd, the trailing unpaired label is dropped. An
// empty (or fully dropped) label list yields "".
func labelSetKey(labels []string) string {
	n := len(labels) / 2 // number of complete k,v pairs (odd trailing dropped)
	if n == 0 {
		return ""
	}

	type kv struct{ k, v string }
	pairs := make([]kv, 0, n)
	for i := 0; i < n; i++ {
		pairs = append(pairs, kv{k: labels[2*i], v: labels[2*i+1]})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })

	parts := make([]string, 0, n)
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, p.k, escapeLabelValue(p.v)))
	}
	return strings.Join(parts, ",")
}

// escapeLabelValue backslash-escapes '\' and '"' in a label value.
func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

// formatValue renders a metric value using the shortest round-trippable
// representation ('g' format), so whole numbers render without a decimal
// point (e.g. 3 -> "3") while large/small values may use scientific
// notation (e.g. 4860000 -> "4.86e+06").
func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
