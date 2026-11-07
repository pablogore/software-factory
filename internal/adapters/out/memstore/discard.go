package memstore

import (
	"context"
	"slices"

	"github.com/pablogore/software-factory/internal/ports"
)

// Discard implements ports.DiscardLog.
func (s *Store) Discard(_ context.Context, d ports.Discarded) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.discarded, func(o ports.Discarded) bool {
		return o.Provider == d.Provider && o.Thread == d.Thread && o.Request == d.Request
	}) {
		return nil
	}
	s.discarded = append(s.discarded, d)
	return nil
}

// Discards implements ports.DiscardLog.
func (s *Store) Discards(_ context.Context) ([]ports.Discarded, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ports.Discarded{}, s.discarded...), nil
}
