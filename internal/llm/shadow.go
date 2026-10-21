package llm

import (
	"context"
	"sync"
	"time"
)

const defaultShadowTimeout = 10 * time.Second

// Shadow runs observations without exposing a moderation decision.
// Configure fields before use; a Shadow must not be copied after first use.
type Shadow struct {
	Providers []Provider
	Timeout   time.Duration // <= 0 uses defaultShadowTimeout
	Max       int
	mu        sync.Mutex
	active    int
}

// Observe immediately rejects a full pool, or starts one bounded observation.
// The slot remains occupied until both report and all provider calls return,
// even when a provider ignores cancellation after the observation timeout.
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
		var calls sync.WaitGroup
		defer func() {
			calls.Wait()
			s.mu.Lock()
			s.active--
			s.mu.Unlock()
		}()
		timeout := s.Timeout
		if timeout <= 0 {
			timeout = defaultShadowTimeout
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()
		out := (Judge{Providers: s.Providers}).adjudicateEach(ctx, text, prompts, &calls)
		report(out.Votes)
	}()
	return true
}
