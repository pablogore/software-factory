// Package sqlite implements ports.JobStore and ports.EventLog on SQLite with
// the pure-Go modernc.org/sqlite driver, so factoryd builds with
// CGO_ENABLED=0.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
`

// Store is a JobStore and EventLog backed by one SQLite file.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path and its schema.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("sqlite: schema: %w", err), db.Close())
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Create implements ports.JobStore.
func (s *Store) Create(ctx context.Context, job domain.Job) error {
	state, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("sqlite: encode job %q: %w", job.ID, err)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO jobs (id, state, version) VALUES (?, ?, 1) ON CONFLICT (id) DO NOTHING`, string(job.ID), string(state))
	if err != nil {
		return fmt.Errorf("sqlite: create job %q: %w", job.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: create job %q: %w", job.ID, err)
	} else if n == 0 {
		return fmt.Errorf("sqlite: %q: %w", job.ID, ports.ErrJobExists)
	}
	return nil
}

// Load implements ports.JobStore.
func (s *Store) Load(ctx context.Context, id domain.JobID) (domain.Job, ports.Version, error) {
	var state string
	var v ports.Version
	err := s.db.QueryRowContext(ctx, `SELECT state, version FROM jobs WHERE id = ?`, string(id)).Scan(&state, &v)
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
	return job, v, nil
}

// Apply implements ports.JobStore: the new state and every event go in one
// transaction, rolled back on the first failure.
func (s *Store) Apply(ctx context.Context, job domain.Job, v ports.Version, evs []domain.Event) (_ ports.Version, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlite: begin apply %q: %w", job.ID, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if err := update(ctx, tx, decide.Apply(job, evs), v); err != nil {
		return 0, err
	}
	for _, ev := range evs {
		if err := insert(ctx, tx, job.ID, ev); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlite: commit apply %q: %w", job.ID, err)
	}
	return v + 1, nil
}

// update saves job only if its row is still at version v. The write comes
// first in the transaction, so SQLite's own write lock orders two writers
// and the late one sees the version the first one left.
func update(ctx context.Context, tx *sql.Tx, job domain.Job, v ports.Version) error {
	state, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("sqlite: encode job %q: %w", job.ID, err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ?, version = version + 1 WHERE id = ? AND version = ?`,
		string(state), string(job.ID), int64(v))
	if err != nil {
		return fmt.Errorf("sqlite: save job %q: %w", job.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: save job %q: %w", job.ID, err)
	} else if n == 1 {
		return nil
	}
	var cur ports.Version
	err = tx.QueryRowContext(ctx, `SELECT version FROM jobs WHERE id = ?`, string(job.ID)).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("sqlite: %q: %w", job.ID, ports.ErrJobNotFound)
	}
	if err != nil {
		return fmt.Errorf("sqlite: save job %q: %w", job.ID, err)
	}
	return fmt.Errorf("sqlite: %q loaded at version %d, now %d: %w", job.ID, v, cur, ports.ErrVersionConflict)
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
	if _, _, err := s.Load(ctx, id); err != nil {
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
