package memstore

import (
	"context"
	"fmt"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// DueActions implements ports.Outbox.
func (s *Store) DueActions(_ context.Context, now time.Time) ([]ports.QueuedAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	due := []ports.QueuedAction{}
	for _, q := range s.outbox {
		if !q.Delivered && !q.Failed && !q.NextAttempt.After(now) {
			due = append(due, q)
		}
	}
	return due, nil
}

// Actions implements ports.Outbox.
func (s *Store) Actions(_ context.Context, id domain.JobID) ([]ports.QueuedAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	qs := []ports.QueuedAction{}
	for _, q := range s.outbox {
		if q.Job == id {
			qs = append(qs, q)
		}
	}
	return qs, nil
}

// MarkActionDelivered implements ports.Outbox.
func (s *Store) MarkActionDelivered(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := s.action(key)
	if err != nil {
		return err
	}
	q.Delivered = true
	q.NextAttempt = time.Time{}
	return nil
}

// MarkActionFailed implements ports.Outbox. Like the SQLite store, a failed
// action keeps no next attempt.
func (s *Store) MarkActionFailed(_ context.Context, key string, f ports.ActionFailure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := s.action(key)
	if err != nil {
		return err
	}
	if f.Failed {
		f.NextAttempt = time.Time{}
	}
	q.ActionFailure = f
	return nil
}

// action is the kept action with key, to change in place.
func (s *Store) action(key string) (*ports.QueuedAction, error) {
	i, ok := s.queued[key]
	if !ok {
		return nil, fmt.Errorf("memstore: action %q: %w", key, ports.ErrActionNotFound)
	}
	return &s.outbox[i], nil
}
