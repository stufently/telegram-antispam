package telegram

import "sync"

// Sequencer serializes jobs per chat_id while running different chats
// concurrently, so updates within one chat are processed in order.
//
// Concurrency contract: Submit is safe to call from many goroutines. Submit is
// also safe to call concurrently with, or after, Wait — once Wait has begun,
// such a Submit is a no-op (its job is dropped, not run, and never panics).
type Sequencer struct {
	mu     sync.Mutex
	queues map[int64]chan func()
	wg     sync.WaitGroup
	quit   chan struct{}
	closed bool
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

	// Send outside the lock so a full queue stalls only this chat's caller.
	// The quit arm prevents a send-on-closed panic if Wait races us.
	select {
	case q <- job:
	case <-s.quit:
	}
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
