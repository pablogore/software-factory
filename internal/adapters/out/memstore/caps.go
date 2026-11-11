package memstore

import (
	"context"

	"github.com/pablogore/software-factory/internal/ports"
)

// SaveIssueCaps implements ports.CapLog. It keeps a copy of the stop, so the
// caller never shares it with what was saved.
func (s *Store) SaveIssueCaps(_ context.Context, c ports.IssueCaps) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.caps[c.Issue] = copyCaps(c)
	return nil
}

// IssueCaps implements ports.CapLog.
func (s *Store) IssueCaps(_ context.Context, n int) (ports.IssueCaps, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.caps[n]
	if !ok {
		return ports.IssueCaps{Issue: n}, nil
	}
	return copyCaps(c), nil
}

func copyCaps(c ports.IssueCaps) ports.IssueCaps {
	if c.Stop != nil {
		stop := *c.Stop
		c.Stop = &stop
	}
	return c
}
