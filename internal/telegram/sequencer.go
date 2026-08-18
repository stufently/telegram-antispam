package telegram

import (
	"log"
	"sync"
	"sync/atomic"
)

// Sequencer serializes jobs per chat_id while running different chats
// concurrently, so updates within one chat are processed in order.
//
// Concurrency contract: Submit is safe to call from many goroutines. Submit is
// also safe to call concurrently with, or after, Wait — once Wait has begun,
// such a Submit is a no-op (its job is dropped, not run, and never panics).
//
// Submit never blocks: if a chat's queue is at capacity, the job is dropped
// (not run) rather than stalling the caller — see the M2-deferred finding
// closed in M3 task 9. A single poll goroutine feeds every chat's queue, so
// a blocking send on one saturated chat would otherwise stall delivery to
// every other chat too.
type Sequencer struct {
	mu       sync.Mutex
	queues   map[int64]chan func()
	wg       sync.WaitGroup
	quit     chan struct{}
	closed   bool
	dropped  int64
	dropOnce sync.Once
}

func NewSequencer() *Sequencer {
	return &Sequencer{
		queues: make(map[int64]chan func()),
		quit:   make(chan struct{}),
	}
}

// Submit enqueues job for chatID. Jobs for the same chatID run in submission
// order on one goroutine; different chats run concurrently. After Wait has
// begun, Submit is a no-op.
//
// Submit is non-blocking: if chatID's queue is already full, the job is
// dropped (Dropped's counter is incremented, and the first drop is logged)
// rather than blocking the caller. The ordering guarantee above is
// unaffected — jobs that ARE accepted still run in submission order.
func (s *Sequencer) Submit(chatID int64, job func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	q, ok := s.queues[chatID]
	if !ok {
		q = make(chan func(), 1024)
		s.queues[chatID] = q
		s.wg.Add(1)
		go s.worker(q)
	}
	s.mu.Unlock()

	// Send outside the lock so a full queue only affects this chat's
	// caller. The quit arm prevents a send-on-closed panic if Wait races
	// us. The default arm makes the whole send non-blocking: if neither the
	// queue nor quit is immediately ready (i.e. the queue is full and
	// shutdown hasn't begun), drop the job instead of stalling the caller.
	select {
	case q <- job:
	case <-s.quit:
	default:
		atomic.AddInt64(&s.dropped, 1)
		s.dropOnce.Do(func() {
			log.Printf("sequencer: dropping job for chat %d, queue full (further drops logged only in Dropped() count)", chatID)
		})
	}
}

// Dropped returns the total number of jobs dropped so far because a chat's
// queue was full when Submit was called.
func (s *Sequencer) Dropped() int64 {
	return atomic.LoadInt64(&s.dropped)
}

func (s *Sequencer) worker(q chan func()) {
	defer s.wg.Done()
	for {
		select {
		case j := <-q:
			j()
		case <-s.quit:
			// Shutdown: drain jobs already queued, then exit.
			for {
				select {
				case j := <-q:
					j()
				default:
					return
				}
			}
		}
	}
}

// Wait signals shutdown and waits for all workers to drain and exit. It is
// idempotent. After Wait returns, further Submit calls are no-ops.
func (s *Sequencer) Wait() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.quit)
	}
	s.mu.Unlock()
	s.wg.Wait()
}
