package config

import (
	"sync"

	"github.com/stufently/telegram-antispam/internal/domain"
)

// Action re-exports the domain enum so config-package tests and callers can
// refer to it without importing domain directly.
type Action = domain.Action

const (
	ActionDeleteMute = domain.ActionDeleteMute
	ActionMute       = domain.ActionMute
	ActionBan        = domain.ActionBan
	ActionDeleteOnly = domain.ActionDeleteOnly
)

// Store holds the live config and swaps it atomically on reload.
type Store struct {
	mu  sync.RWMutex
	cur *Config
}

func NewStore(initial *Config) *Store { return &Store{cur: initial} }

// Current returns the live config. Never returns a partially-parsed value.
func (s *Store) Current() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Swap replaces the live config.
func (s *Store) Swap(candidate *Config) {
	s.mu.Lock()
	s.cur = candidate
	s.mu.Unlock()
}

// tryReload parses+validates path; on success swaps, on failure keeps the old
// config and returns the error.
func (s *Store) tryReload(path string) error {
	candidate, err := Load(path)
	if err != nil {
		return err
	}
	s.Swap(candidate)
	return nil
}
