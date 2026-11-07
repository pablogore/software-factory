// Package memstore is the in-memory fake of ports.JobStore, ports.EventLog
// and ports.EventFeed for slice tests. It passes the same contract suite as the
// SQLite store (internal/adapters/out/sqlite).
package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
	mu        sync.Mutex
	jobs      map[domain.JobID][]byte
	versions  map[domain.JobID]ports.Version
	log       []logged // every job's events in saved order; Seq is index+1
	processed map[string]map[ports.Seq]bool
	maxReason int
}

// logged is one saved event and the job it belongs to.
type logged struct {
	job domain.JobID
	env eventjson.Envelope
}

// Option sets how New configures the store.
type Option func(*Store)

// WithMaxReason keeps at most n characters of an override's reason, instead
// of eventjson.DefaultMaxReason. n must be at least 1.
func WithMaxReason(n int) Option {
	return func(s *Store) { s.maxReason = n }
}

// New returns an empty store, or an error for an invalid option.
func New(opts ...Option) (*Store, error) {
	s := &Store{
		jobs:      map[domain.JobID][]byte{},
		versions:  map[domain.JobID]ports.Version{},
		processed: map[string]map[ports.Seq]bool{},
		maxReason: eventjson.DefaultMaxReason,
	}
	for _, opt := range opts {
		opt(s)
	}
	if err := eventjson.CheckMaxReason(s.maxReason); err != nil {
		return nil, fmt.Errorf("memstore: new: %w", err)
	}
	return s, nil
}

// Create implements ports.JobStore.
func (s *Store) Create(_ context.Context, job domain.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("memstore: create: %w", err)
	}
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
	return s.load(id)
}

func (s *Store) load(id domain.JobID) (domain.Job, ports.Version, error) {
	data, ok := s.jobs[id]
	if !ok {
		return domain.Job{}, 0, fmt.Errorf("memstore: %q: %w", id, ports.ErrJobNotFound)
	}
	var job domain.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return domain.Job{}, 0, fmt.Errorf("memstore: decode job %q: %w", id, err)
	}
	if err := job.Validate(); err != nil {
		return domain.Job{}, 0, fmt.Errorf("memstore: load: %w", err)
	}
	return job, s.versions[id], nil
}

// Apply implements ports.JobStore. Like the SQLite store it decides on the
// job as saved, never one the caller hands in, and everything is encoded
// before anything is kept, so a failure keeps nothing.
func (s *Store) Apply(_ context.Context, id domain.JobID, v ports.Version, p domain.Pipeline, ev domain.Event) ([]domain.Event, ports.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return nil, 0, fmt.Errorf("memstore: %q: %w", id, ports.ErrJobNotFound)
	}
	if cur := s.versions[id]; cur != v {
		return nil, 0, fmt.Errorf("memstore: %q loaded at version %d, now %d: %w", id, v, cur, ports.ErrVersionConflict)
	}
	job, _, err := s.load(id)
	if err != nil {
		return nil, 0, err
	}
	evs, err := decide.Decide(job, ev, p)
	if err != nil {
		return nil, 0, fmt.Errorf("memstore: job %q: %w", id, err)
	}
	if len(evs) == 0 {
		return nil, v, nil
	}
	evs = eventjson.CapReasons(evs, s.maxReason)
	next := decide.Apply(job, evs)
	if err := next.Validate(); err != nil {
		return nil, 0, fmt.Errorf("memstore: save: %w", err)
	}
	data, err := json.Marshal(next)
	if err != nil {
		return nil, 0, fmt.Errorf("memstore: encode job %q: %w", id, err)
	}
	envs := make([]eventjson.Envelope, 0, len(evs))
	for _, ev := range evs {
		env, err := eventjson.Encode(ev)
		if err != nil {
			return nil, 0, fmt.Errorf("memstore: job %q: %w", id, err)
		}
		envs = append(envs, env)
	}
	s.jobs[id] = data
	s.versions[id] = v + 1
	s.append(id, envs)
	return evs, v + 1, nil
}

// History implements ports.EventLog.
func (s *Store) History(_ context.Context, id domain.JobID) ([]ports.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return nil, fmt.Errorf("memstore: %q: %w", id, ports.ErrJobNotFound)
	}
	recs := []ports.Record{}
	for _, l := range s.log {
		if l.job != id {
			continue
		}
		ev, err := eventjson.Decode(l.env)
		if err != nil {
			return nil, fmt.Errorf("memstore: job %q: %w", id, err)
		}
		recs = append(recs, ports.Record{Event: ev, At: l.env.At})
	}
	return recs, nil
}

func (s *Store) append(id domain.JobID, envs []eventjson.Envelope) {
	for _, env := range envs {
		s.log = append(s.log, logged{job: id, env: env})
	}
}

// Pending implements ports.EventFeed.
func (s *Store) Pending(_ context.Context, subscriber string, names []string) ([]ports.Saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := []ports.Saved{}
	for i, l := range s.log {
		seq := ports.Seq(i + 1)
		if !slices.Contains(names, l.env.Name) || s.processed[subscriber][seq] {
			continue
		}
		ev, err := eventjson.Decode(l.env)
		if err != nil {
			return nil, fmt.Errorf("memstore: event %d: %w", seq, err)
		}
		saved = append(saved, ports.Saved{Seq: seq, Record: ports.Record{Event: ev, At: l.env.At}})
	}
	return saved, nil
}

// MarkProcessed implements ports.EventFeed.
func (s *Store) MarkProcessed(_ context.Context, subscriber string, seq ports.Seq) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.processed[subscriber] == nil {
		s.processed[subscriber] = map[ports.Seq]bool{}
	}
	s.processed[subscriber][seq] = true
	return nil
}
