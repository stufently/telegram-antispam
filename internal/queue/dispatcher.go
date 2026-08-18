package queue

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RetryAfter is returned by a Job.Run to signal Telegram 429 backoff. The
// dispatcher re-enqueues the same job after sleeping Seconds.
type RetryAfter struct{ Seconds int }

func (r RetryAfter) Error() string { return fmt.Sprintf("retry after %ds", r.Seconds) }

// Dispatcher runs queued jobs under a global and per-chat rate limit,
// retrying jobs that fail with RetryAfter.
type Dispatcher struct {
	q       *Queue
	global  *rate.Limiter
	perChat func(chat int64) *rate.Limiter

	// jmu guards jobs, the seq->chat map used to pick the per-chat limiter
	// for a popped job. jobs is written in Submit and read+deleted in Run;
	// jmu is the sole lock protecting it so those accesses are race-free
	// independent of q's own lock. Ordering (see Submit/pushSeq) ensures the
	// mapping for a seq is always written before that job becomes visible
	// to pop, so Run never observes a seq with no mapping.
	jmu  sync.Mutex
	jobs map[uint64]int64

	// sleep is the retry-backoff wait; ctx-aware, overridable in tests.
	sleep func(context.Context, time.Duration)

	wake chan struct{}
}

// NewDispatcher creates a Dispatcher that rate-limits via global (applied to
// every job) and perChat (applied per chat ID, looked up per job).
func NewDispatcher(global *rate.Limiter, perChat func(chat int64) *rate.Limiter) *Dispatcher {
	return &Dispatcher{
		q:       New(),
		global:  global,
		perChat: perChat,
		jobs:    map[uint64]int64{},
		sleep: func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		},
		wake: make(chan struct{}, 1),
	}
}

// Submit enqueues j for chat.
func (d *Dispatcher) Submit(chat int64, j Job) {
	j.seq = d.q.reserveSeq()

	// Record the seq->chat mapping BEFORE the job is pushed onto the heap,
	// so Run can never pop a job whose mapping isn't there yet.
	d.jmu.Lock()
	d.jobs[j.seq] = chat
	d.jmu.Unlock()

	d.q.pushSeq(j)

	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run processes jobs until ctx is cancelled. On RetryAfter it re-enqueues
// the same job after sleeping; on any other error it logs and drops the job.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		j, ok := d.q.pop()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
				continue
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}

		d.jmu.Lock()
		chat := d.jobs[j.seq]
		delete(d.jobs, j.seq)
		d.jmu.Unlock()

		if err := d.global.Wait(ctx); err != nil {
			return
		}
		if lim := d.perChat(chat); lim != nil {
			if err := lim.Wait(ctx); err != nil {
				return
			}
		}

		if err := j.Run(ctx); err != nil {
			if ra, ok := err.(RetryAfter); ok {
				d.sleep(ctx, time.Duration(ra.Seconds)*time.Second)
				if ctx.Err() != nil {
					return
				}
				d.Submit(chat, j) // re-enqueue same job
				continue
			}
			log.Printf("queue job failed (chat %d): %v", chat, err)
		}
	}
}
