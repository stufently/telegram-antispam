package queue

import (
	"context"
	"testing"
)

func TestPopOrdersByPriorityThenFIFO(t *testing.T) {
	q := New()
	mk := func(p Priority, tag string) Job {
		return Job{Priority: p, Run: func(context.Context) error { return nil }, tag: tag}
	}
	q.Push(mk(PrioLow, "low1"))
	q.Push(mk(PrioHigh, "high1"))
	q.Push(mk(PrioNormal, "norm1"))
	q.Push(mk(PrioHigh, "high2"))
	var order []string
	for {
		j, ok := q.pop()
		if !ok {
			break
		}
		order = append(order, j.tag)
	}
	want := []string{"high1", "high2", "norm1", "low1"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v want %v", order, want)
		}
	}
}
