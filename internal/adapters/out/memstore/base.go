package memstore

import (
	"context"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// RecordBase implements ports.BaseLog.
func (s *Store) RecordBase(_ context.Context, job domain.JobID, commit string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if got, ok := s.bases[job]; ok {
		if got != commit {
			return fmt.Errorf("memstore: base of job %q: %w: %s", job, ports.ErrBaseRecorded, got)
		}
		return nil
	}
	s.bases[job] = commit
	return nil
}

// Base implements ports.BaseLog.
func (s *Store) Base(_ context.Context, job domain.JobID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	commit, ok := s.bases[job]
	if !ok {
		return "", fmt.Errorf("memstore: job %q: %w", job, ports.ErrNoBase)
	}
	return commit, nil
}
