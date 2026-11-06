// Package memstore is the in-memory fake of ports.JobStore and
// ports.EventLog for slice tests. It passes the same contract suite as the
// SQLite store (internal/adapters/out/sqlite).
package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pablogore/software-factory/internal/adapters/out/eventjson"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/ports"
)

// Store keeps jobs and events in memory. Like the SQLite store it keeps them
// encoded, so a caller never shares slices with what was saved and both
// refuse the same unknown events.
type Store struct {
	mu       sync.Mutex
	jobs     map[domain.JobID][]byte
	versions map[domain.JobID]ports.Version
	events   map[domain.JobID][]eventjson.Envelope
}

// New returns an empty store.
func New() *Store {
	return &Store{
		jobs:     map[domain.JobID][]byte{},
		versions: map[domain.JobID]ports.Version{},
		events:   map[domain.JobID][]eventjson.Envelope{},
	}
}

// Create implements ports.JobStore.
func (s *Store) Create(_ context.Context, job domain.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[job.ID]; ok {
		return fmt.Errorf("memstore: %q: %w", job.ID, ports.ErrJobExists)
	}
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("memstore: encode job %q: %w", job.ID, err)
	}
	s.jobs[job.ID] = data
	s.versions[job.ID] = 1
	return nil
}

// Load implements ports.JobStore.
func (s *Store) Load(_ context.Context, id domain.JobID) (domain.Job, ports.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.jobs[id]
	if !ok {
		return domain.Job{}, 0, fmt.Errorf("memstore: %q: %w", id, ports.ErrJobNotFound)
	}
	var job domain.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return domain.Job{}, 0, fmt.Errorf("memstore: decode job %q: %w", id, err)
	}
	return job, s.versions[id], nil
}

// Apply implements ports.JobStore. Everything is encoded before anything is
// kept, so a failure keeps nothing.
func (s *Store) Apply(_ context.Context, job domain.Job, v ports.Version, evs []domain.Event) (ports.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[job.ID]; !ok {
		return 0, fmt.Errorf("memstore: %q: %w", job.ID, ports.ErrJobNotFound)
	}
	if cur := s.versions[job.ID]; cur != v {
		return 0, fmt.Errorf("memstore: %q loaded at version %d, now %d: %w", job.ID, v, cur, ports.ErrVersionConflict)
	}
	data, err := json.Marshal(decide.Apply(job, evs))
	if err != nil {
		return 0, fmt.Errorf("memstore: encode job %q: %w", job.ID, err)
	}
	envs := make([]eventjson.Envelope, 0, len(evs))
	for _, ev := range evs {
		env, err := eventjson.Encode(ev)
		if err != nil {
			return 0, fmt.Errorf("memstore: job %q: %w", job.ID, err)
		}
		envs = append(envs, env)
	}
	s.jobs[job.ID] = data
	s.versions[job.ID] = v + 1
	s.events[job.ID] = append(s.events[job.ID], envs...)
	return v + 1, nil
}

// History implements ports.EventLog.
func (s *Store) History(_ context.Context, id domain.JobID) ([]ports.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return nil, fmt.Errorf("memstore: %q: %w", id, ports.ErrJobNotFound)
	}
	recs := make([]ports.Record, 0, len(s.events[id]))
	for _, env := range s.events[id] {
		ev, err := eventjson.Decode(env)
		if err != nil {
			return nil, fmt.Errorf("memstore: job %q: %w", id, err)
		}
		recs = append(recs, ports.Record{Event: ev, At: env.At})
	}
	return recs, nil
}
