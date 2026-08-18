package telegram

import "sync"

// Sequencer serializes jobs per chat_id while running different chats
// concurrently, so updates within one chat are processed in order.
type Sequencer struct {
	mu     sync.Mutex
	queues map[int64]chan func()
	wg     sync.WaitGroup
}

func NewSequencer() *Sequencer {
	return &Sequencer{queues: make(map[int64]chan func())}
}

func (s *Sequencer) Submit(chatID int64, job func()) {
	s.mu.Lock()
	q, ok := s.queues[chatID]
	if !ok {
		q = make(chan func(), 1024)
		s.queues[chatID] = q
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for j := range q {
				j()
			}
		}()
	}
	s.mu.Unlock()
	q <- job
}

// Wait closes all queues and waits for workers to drain. Call once at shutdown.
func (s *Sequencer) Wait() {
	s.mu.Lock()
	for _, q := range s.queues {
		close(q)
	}
	s.queues = make(map[int64]chan func())
	s.mu.Unlock()
	s.wg.Wait()
}
