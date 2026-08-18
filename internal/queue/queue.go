// Package queue is the outbound work queue: priority-ordered, rate-limited,
// retrying. All Telegram side effects flow through it (spec §11).
package queue

import (
	"container/heap"
	"context"
	"sync"
)

type Priority int

const (
	PrioHigh   Priority = 0 // delete / ban
	PrioNormal Priority = 1 // notifications
	PrioLow    Priority = 2 // TTL cleanup
)

// Job is one unit of outbound work.
type Job struct {
	Priority Priority
	Run      func(ctx context.Context) error
	tag      string // test-only label
	seq      uint64 // FIFO tiebreaker within a priority
}

type pqueue []Job

func (p pqueue) Len() int { return len(p) }
func (p pqueue) Less(i, j int) bool {
	if p[i].Priority != p[j].Priority {
		return p[i].Priority < p[j].Priority
	}
	return p[i].seq < p[j].seq
}
func (p pqueue) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p *pqueue) Push(x any)        { *p = append(*p, x.(Job)) }
func (p *pqueue) Pop() any {
	old := *p
	n := len(old)
	j := old[n-1]
	*p = old[:n-1]
	return j
}

// Queue is a thread-safe priority queue of Jobs.
type Queue struct {
	mu   sync.Mutex
	pq   pqueue
	next uint64
}

func New() *Queue { return &Queue{} }

func (q *Queue) Push(j Job) {
	q.mu.Lock()
	j.seq = q.next
	q.next++
	heap.Push(&q.pq, j)
	q.mu.Unlock()
}

func (q *Queue) pop() (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pq.Len() == 0 {
		return Job{}, false
	}
	return heap.Pop(&q.pq).(Job), true
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pq.Len()
}
