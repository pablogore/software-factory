// Package memstore is the in-memory fake of ports.JobStore, ports.EventLog,
// ports.EventFeed, ports.DeadEvents, ports.Outbox, ports.DiscardLog and
// ports.BaseLog for slice tests. It passes the same contract suite as the
// SQLite store (internal/adapters/out/sqlite).
package memstore

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

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
	// failed holds each subscriber's failed deliveries, retrying and dead.
	failed map[string]map[ports.Seq]ports.Delivery
	// outbox holds every job's actions in saved order; queued finds one by
	// its key.
	outbox    []ports.QueuedAction
	queued    map[string]int
	discarded []ports.Discarded
	bases     map[domain.JobID]string
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
		failed:    map[string]map[ports.Seq]ports.Delivery{},
		queued:    map[string]int{},
		bases:     map[domain.JobID]string{},
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

// Create implements ports.JobStore. Like the SQLite store it checks the job
// and every action before keeping anything.
func (s *Store) Create(_ context.Context, job domain.Job, acts ...ports.Action) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("memstore: create: %w", err)
	}
	for _, a := range acts {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("memstore: job %q: %w", job.ID, err)
		}
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
	s.enqueue(job.ID, acts)
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
// job as saved, never one the caller hands in, and everything is encoded and
// every action checked before anything is kept, so a failure keeps nothing.
func (s *Store) Apply(_ context.Context, id domain.JobID, v ports.Version, p domain.Pipeline, ev domain.Event, acts ...ports.Action) ([]domain.Event, ports.Version, error) {
	for _, a := range acts {
		if err := a.Validate(); err != nil {
			return nil, 0, fmt.Errorf("memstore: job %q: %w", id, err)
		}
	}
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
	if err := s.keep(job, v, evs); err != nil {
		return nil, 0, err
	}
	s.enqueue(id, acts)
	return evs, v + 1, nil
}

// keep saves decide.Apply(job, evs) as version v+1 with evs, once everything
// is encoded, so a failure keeps nothing.
func (s *Store) keep(job domain.Job, v ports.Version, evs []domain.Event) error {
	id := job.ID
	next := decide.Apply(job, evs)
	if err := next.Validate(); err != nil {
		return fmt.Errorf("memstore: save: %w", err)
	}
	data, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("memstore: encode job %q: %w", id, err)
	}
	envs := make([]eventjson.Envelope, 0, len(evs))
	for _, ev := range evs {
		env, err := eventjson.Encode(ev)
		if err != nil {
			return fmt.Errorf("memstore: job %q: %w", id, err)
		}
		envs = append(envs, env)
	}
	s.jobs[id] = data
	s.versions[id] = v + 1
	s.append(id, envs)
	return nil
}

// enqueue keeps each of acts pending for job id, unless its key is kept
// already.
func (s *Store) enqueue(id domain.JobID, acts []ports.Action) {
	for _, a := range acts {
		if _, ok := s.queued[a.Key]; ok {
			continue
		}
		s.queued[a.Key] = len(s.outbox)
		s.outbox = append(s.outbox, ports.QueuedAction{Job: id, Action: a})
	}
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

// jobOf is the job the event at seq belongs to.
func (s *Store) jobOf(seq ports.Seq) (domain.JobID, error) {
	if seq < 1 || int(seq) > len(s.log) {
		return "", fmt.Errorf("memstore: event %d: %w", seq, ports.ErrEventNotFound)
	}
	return s.log[seq-1].job, nil
}

// MarkProcessed implements ports.EventFeed.
func (s *Store) MarkProcessed(_ context.Context, subscriber string, seq ports.Seq) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.jobOf(seq); err != nil {
		return err
	}
	s.markProcessed(subscriber, seq)
	return nil
}

func (s *Store) markProcessed(subscriber string, seq ports.Seq) {
	if s.processed[subscriber] == nil {
		s.processed[subscriber] = map[ports.Seq]bool{}
	}
	s.processed[subscriber][seq] = true
	delete(s.failed[subscriber], seq)
}

// MarkFailed implements ports.EventFeed. Like the dead_events table, a dead
// delivery keeps no next attempt.
func (s *Store) MarkFailed(_ context.Context, subscriber string, seq ports.Seq, f ports.Failure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.jobOf(seq)
	if err != nil {
		return err
	}
	if f.Dead {
		f.NextAttempt = time.Time{}
	}
	if s.failed[subscriber] == nil {
		s.failed[subscriber] = map[ports.Seq]ports.Delivery{}
	}
	s.failed[subscriber][seq] = ports.Delivery{Subscriber: subscriber, Seq: seq, Job: job, Failure: f}
	return nil
}

// Deliveries implements ports.EventFeed.
func (s *Store) Deliveries(_ context.Context, subscriber string) ([]ports.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds := []ports.Delivery{}
	for _, d := range s.failed[subscriber] {
		ds = append(ds, d)
	}
	slices.SortFunc(ds, func(a, b ports.Delivery) int { return cmp.Compare(a.Seq, b.Seq) })
	return ds, nil
}

// RetryDead implements ports.DeadEvents.
func (s *Store) RetryDead(_ context.Context, subscriber string, seq ports.Seq) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.undead(subscriber, seq)
}

// DiscardDead implements ports.DeadEvents.
func (s *Store) DiscardDead(_ context.Context, subscriber string, seq ports.Seq) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.undead(subscriber, seq); err != nil {
		return err
	}
	s.markProcessed(subscriber, seq)
	return nil
}

// undead removes subscriber's dead event at seq, or fails with ErrNotDead.
func (s *Store) undead(subscriber string, seq ports.Seq) error {
	if d, ok := s.failed[subscriber][seq]; !ok || !d.Dead {
		return fmt.Errorf("memstore: event %d of %q: %w", seq, subscriber, ports.ErrNotDead)
	}
	delete(s.failed[subscriber], seq)
	return nil
}
