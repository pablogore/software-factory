// Package sqlite implements ports.JobStore, ports.EventLog, ports.EventFeed,
// ports.DeadEvents, ports.Outbox, ports.DiscardLog, ports.BaseLog and
// ports.BrakeLog on SQLite with the pure-Go modernc.org/sqlite driver, so
// factoryd builds with CGO_ENABLED=0.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/pablogore/software-factory/internal/adapters/out/eventjson"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/ports"
)

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id      TEXT PRIMARY KEY,
	state   TEXT NOT NULL,
	version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
	seq    INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id TEXT NOT NULL REFERENCES jobs(id),
	name   TEXT NOT NULL,
	at     TEXT NOT NULL,
	data   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_job ON events(job_id, seq);
CREATE TABLE IF NOT EXISTS processed (
	subscriber TEXT NOT NULL,
	job_id     TEXT NOT NULL,
	seq        INTEGER NOT NULL REFERENCES events(seq),
	PRIMARY KEY (subscriber, seq)
);
CREATE TABLE IF NOT EXISTS deliveries (
	subscriber   TEXT NOT NULL,
	job_id       TEXT NOT NULL,
	seq          INTEGER NOT NULL REFERENCES events(seq),
	attempts     INTEGER NOT NULL,
	last_error   TEXT NOT NULL,
	next_attempt TEXT NOT NULL,
	PRIMARY KEY (subscriber, seq)
);
CREATE TABLE IF NOT EXISTS dead_events (
	subscriber TEXT NOT NULL,
	job_id     TEXT NOT NULL,
	seq        INTEGER NOT NULL REFERENCES events(seq),
	attempts   INTEGER NOT NULL,
	last_error TEXT NOT NULL,
	PRIMARY KEY (subscriber, seq)
);
CREATE TABLE IF NOT EXISTS outbox (
	seq          INTEGER PRIMARY KEY AUTOINCREMENT,
	key          TEXT NOT NULL UNIQUE,
	job_id       TEXT NOT NULL REFERENCES jobs(id),
	kind         TEXT NOT NULL,
	payload      TEXT NOT NULL,
	delivered    INTEGER NOT NULL DEFAULT 0,
	attempts     INTEGER NOT NULL DEFAULT 0,
	last_error   TEXT NOT NULL DEFAULT '',
	next_attempt TEXT NOT NULL DEFAULT '',
	failed       INTEGER NOT NULL DEFAULT 0,
	result_lost  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS discarded (
	seq      INTEGER PRIMARY KEY AUTOINCREMENT,
	provider TEXT NOT NULL,
	thread   TEXT NOT NULL,
	request  TEXT NOT NULL,
	author   TEXT NOT NULL,
	at       TEXT NOT NULL,
	UNIQUE (provider, thread, request)
);
CREATE TABLE IF NOT EXISTS bases (
	job_id      TEXT PRIMARY KEY,
	commit_name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS brake (
	id      INTEGER PRIMARY KEY CHECK (id = 1),
	engaged INTEGER NOT NULL
);
`

// Store is a JobStore, EventLog, EventFeed, DeadEvents, Outbox, DiscardLog,
// BaseLog and BrakeLog backed by one SQLite file.
type Store struct {
	db        *sql.DB
	maxReason int
}

// Option sets how Open configures the store.
type Option func(*Store)

// WithMaxReason keeps at most n characters of an override's reason, instead
// of eventjson.DefaultMaxReason. n must be at least 1.
func WithMaxReason(n int) Option {
	return func(s *Store) { s.maxReason = n }
}

// Open opens or creates the database at path and its schema.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	st := &Store{maxReason: eventjson.DefaultMaxReason}
	for _, opt := range opts {
		opt(st)
	}
	if err := eventjson.CheckMaxReason(st.maxReason); err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("sqlite: schema: %w", err), db.Close())
	}
	st.db = db
	return st, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Create implements ports.JobStore: the job and its actions are saved in one
// transaction, rolled back on the first failure. The insert comes first, so
// of two creates of one job the late one finds it and saves nothing.
func (s *Store) Create(ctx context.Context, job domain.Job, acts ...ports.Action) (err error) {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("sqlite: create: %w", err)
	}
	for _, a := range acts {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("sqlite: job %q: %w", job.ID, err)
		}
	}
	state, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("sqlite: encode job %q: %w", job.ID, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin create %q: %w", job.ID, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	res, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, state, version) VALUES (?, ?, 1) ON CONFLICT (id) DO NOTHING`, string(job.ID), string(state))
	if err != nil {
		return fmt.Errorf("sqlite: create job %q: %w", job.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: create job %q: %w", job.ID, err)
	} else if n == 0 {
		return fmt.Errorf("sqlite: %q: %w", job.ID, ports.ErrJobExists)
	}
	for _, a := range acts {
		if err := enqueue(ctx, tx, job.ID, a); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit create %q: %w", job.ID, err)
	}
	return nil
}

// querier is what Load needs from either the database or a transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Load implements ports.JobStore.
func (s *Store) Load(ctx context.Context, id domain.JobID) (domain.Job, ports.Version, error) {
	return load(ctx, s.db, id)
}

func load(ctx context.Context, q querier, id domain.JobID) (domain.Job, ports.Version, error) {
	var state string
	var v ports.Version
	err := q.QueryRowContext(ctx, `SELECT state, version FROM jobs WHERE id = ?`, string(id)).Scan(&state, &v)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Job{}, 0, fmt.Errorf("sqlite: %q: %w", id, ports.ErrJobNotFound)
	}
	if err != nil {
		return domain.Job{}, 0, fmt.Errorf("sqlite: load job %q: %w", id, err)
	}
	var job domain.Job
	if err := json.Unmarshal([]byte(state), &job); err != nil {
		return domain.Job{}, 0, fmt.Errorf("sqlite: decode job %q: %w", id, err)
	}
	if err := job.Validate(); err != nil {
		return domain.Job{}, 0, fmt.Errorf("sqlite: load: %w", err)
	}
	return job, v, nil
}

// Apply implements ports.JobStore: the job is read, decided and saved with
// Decide's events and the actions asked for in one transaction, rolled back
// on the first failure.
func (s *Store) Apply(ctx context.Context, id domain.JobID, v ports.Version, p domain.Pipeline, ev domain.Event, acts ...ports.Action) (_ []domain.Event, _ ports.Version, err error) {
	for _, a := range acts {
		if err := a.Validate(); err != nil {
			return nil, 0, fmt.Errorf("sqlite: job %q: %w", id, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: begin apply %q: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if err := claim(ctx, tx, id, v); err != nil {
		return nil, 0, err
	}
	// Read inside the transaction, after the claim: Decide sees the job as
	// saved at v, never a state the caller hands in.
	job, _, err := load(ctx, tx, id)
	if err != nil {
		return nil, 0, err
	}
	evs, err := decide.Decide(job, ev, p)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: job %q: %w", id, err)
	}
	if len(evs) == 0 {
		return nil, v, tx.Rollback()
	}
	evs = eventjson.CapReasons(evs, s.maxReason)
	if err := record(ctx, tx, job, evs); err != nil {
		return nil, 0, err
	}
	for _, a := range acts {
		if err := enqueue(ctx, tx, id, a); err != nil {
			return nil, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("sqlite: commit apply %q: %w", id, err)
	}
	return evs, v + 1, nil
}

// claim moves the job's row from version v to v+1, or fails with
// ErrVersionConflict. The write comes first in the transaction, so SQLite's
// own write lock orders two writers and the late one sees the version the
// first one left.
func claim(ctx context.Context, tx *sql.Tx, id domain.JobID, v ports.Version) error {
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET version = version + 1 WHERE id = ? AND version = ?`, string(id), int64(v))
	if err != nil {
		return fmt.Errorf("sqlite: save job %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: save job %q: %w", id, err)
	} else if n == 1 {
		return nil
	}
	var cur ports.Version
	err = tx.QueryRowContext(ctx, `SELECT version FROM jobs WHERE id = ?`, string(id)).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("sqlite: %q: %w", id, ports.ErrJobNotFound)
	}
	if err != nil {
		return fmt.Errorf("sqlite: save job %q: %w", id, err)
	}
	return fmt.Errorf("sqlite: %q loaded at version %d, now %d: %w", id, v, cur, ports.ErrVersionConflict)
}

// record saves decide.Apply(job, evs) and evs, in the claimed job's row.
func record(ctx context.Context, tx *sql.Tx, job domain.Job, evs []domain.Event) error {
	if err := save(ctx, tx, decide.Apply(job, evs)); err != nil {
		return err
	}
	for _, ev := range evs {
		if err := insert(ctx, tx, job.ID, ev); err != nil {
			return err
		}
	}
	return nil
}

// save writes the job's new state, which must keep its invariants.
func save(ctx context.Context, tx *sql.Tx, job domain.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("sqlite: save: %w", err)
	}
	state, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("sqlite: encode job %q: %w", job.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ? WHERE id = ?`, string(state), string(job.ID)); err != nil {
		return fmt.Errorf("sqlite: save job %q: %w", job.ID, err)
	}
	return nil
}

func insert(ctx context.Context, tx *sql.Tx, id domain.JobID, ev domain.Event) error {
	env, err := eventjson.Encode(ev)
	if err != nil {
		return fmt.Errorf("sqlite: job %q: %w", id, err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events (job_id, name, at, data) VALUES (?, ?, ?, ?)`,
		string(id), env.Name, env.At.Format(time.RFC3339Nano), string(env.Data))
	if err != nil {
		return fmt.Errorf("sqlite: save %s of job %q: %w", env.Name, id, err)
	}
	return nil
}

// History implements ports.EventLog.
func (s *Store) History(ctx context.Context, id domain.JobID) ([]ports.Record, error) {
	if err := exists(ctx, s.db, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, at, data FROM events WHERE job_id = ? ORDER BY seq`, string(id))
	if err != nil {
		return nil, fmt.Errorf("sqlite: history of %q: %w", id, err)
	}
	defer func() { _ = rows.Close() }() // rows.Err reports what matters
	recs := []ports.Record{}
	for rows.Next() {
		var env eventjson.Envelope
		var at, data string
		if err := rows.Scan(&env.Name, &at, &data); err != nil {
			return nil, fmt.Errorf("sqlite: history of %q: %w", id, err)
		}
		if env.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("sqlite: time of %s in job %q: %w", env.Name, id, err)
		}
		env.Data = []byte(data)
		ev, err := eventjson.Decode(env)
		if err != nil {
			return nil, fmt.Errorf("sqlite: job %q: %w", id, err)
		}
		recs = append(recs, ports.Record{Event: ev, At: env.At})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: history of %q: %w", id, err)
	}
	return recs, nil
}

// exists reports a missing job as ErrJobNotFound. History reads no state, so
// a job that fails Validate still shows the events that led there.
func exists(ctx context.Context, q querier, id domain.JobID) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM jobs WHERE id = ?`, string(id)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("sqlite: %q: %w", id, ports.ErrJobNotFound)
	}
	if err != nil {
		return fmt.Errorf("sqlite: history of %q: %w", id, err)
	}
	return nil
}

// Pending implements ports.EventFeed. The events table's seq is the feed's
// Seq: AUTOINCREMENT never reuses one, so saved order is seq order.
func (s *Store) Pending(ctx context.Context, subscriber string, names []string) ([]ports.Saved, error) {
	saved := []ports.Saved{}
	if len(names) == 0 {
		return saved, nil
	}
	args := []any{subscriber}
	for _, n := range names {
		args = append(args, n)
	}
	q := `SELECT seq, name, at, data FROM events e
		WHERE NOT EXISTS (SELECT 1 FROM processed p WHERE p.subscriber = ? AND p.seq = e.seq)
		AND name IN (?` + strings.Repeat(", ?", len(names)-1) + `)
		ORDER BY seq`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: pending for %q: %w", subscriber, err)
	}
	defer func() { _ = rows.Close() }() // rows.Err reports what matters
	for rows.Next() {
		var sv ports.Saved
		var env eventjson.Envelope
		var at, data string
		if err := rows.Scan(&sv.Seq, &env.Name, &at, &data); err != nil {
			return nil, fmt.Errorf("sqlite: pending for %q: %w", subscriber, err)
		}
		if env.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("sqlite: time of event %d: %w", sv.Seq, err)
		}
		env.Data = []byte(data)
		if sv.Event, err = eventjson.Decode(env); err != nil {
			return nil, fmt.Errorf("sqlite: event %d: %w", sv.Seq, err)
		}
		sv.At = env.At
		saved = append(saved, sv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: pending for %q: %w", subscriber, err)
	}
	return saved, nil
}
