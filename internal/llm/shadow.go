package llm

import (
	"context"
	"sync"
	"time"
)

// Shadow runs observations without exposing a moderation decision.
// Configure fields before use; a Shadow must not be copied after first use.
type Shadow struct {
	Providers []Provider
	Timeout   time.Duration
	Max       int
	mu        sync.Mutex
	active    int
}

// Observe immediately rejects a full pool, or starts one bounded observation.
// The slot remains occupied until report returns, including after a timeout.
func (s *Shadow) Observe(parent context.Context, text string, prompts []string, report func([]Vote)) bool {
	s.mu.Lock()
	max := s.Max
	if max <= 0 {
		max = 1
	}
	if s.active >= max {
		s.mu.Unlock()
		return false
	}
	s.active++
	s.mu.Unlock()
	// Own the prompt slice before returning to the caller.
	prompts = append([]string(nil), prompts...)
	go func() {
		defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(parent, s.Timeout)
		defer cancel()
		out := (Judge{Providers: s.Providers}).AdjudicateEach(ctx, text, prompts)
		report(out.Votes)
	}()
	return true
}
