package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// enqueue saves a, pending, for job id. A saved key keeps its row: the action
// was asked for already.
func enqueue(ctx context.Context, tx *sql.Tx, id domain.JobID, a ports.Action) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox (key, job_id, kind, payload) VALUES (?, ?, ?, ?) ON CONFLICT (key) DO NOTHING`,
		a.Key, string(id), a.Kind, a.Payload)
	if err != nil {
		return fmt.Errorf("sqlite: save action %q of job %q: %w", a.Key, id, err)
	}
	return nil
}

const queuedColumns = `job_id, key, kind, payload, delivered, attempts, last_error, next_attempt, failed`

// DueActions implements ports.Outbox. Times are compared after parsing, since
// RFC 3339 text with trimmed fractions does not sort as time does.
func (s *Store) DueActions(ctx context.Context, now time.Time) ([]ports.QueuedAction, error) {
	all, err := s.queued(ctx, `SELECT `+queuedColumns+` FROM outbox WHERE delivered = 0 AND failed = 0 ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: due actions: %w", err)
	}
	due := []ports.QueuedAction{}
	for _, q := range all {
		if !q.NextAttempt.After(now) {
			due = append(due, q)
		}
	}
	return due, nil
}

// Actions implements ports.Outbox.
func (s *Store) Actions(ctx context.Context, id domain.JobID) ([]ports.QueuedAction, error) {
	qs, err := s.queued(ctx, `SELECT `+queuedColumns+` FROM outbox WHERE job_id = ? ORDER BY seq`, string(id))
	if err != nil {
		return nil, fmt.Errorf("sqlite: actions of %q: %w", id, err)
	}
	return qs, nil
}

func (s *Store) queued(ctx context.Context, query string, args ...any) ([]ports.QueuedAction, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err reports what matters
	qs := []ports.QueuedAction{}
	for rows.Next() {
		var q ports.QueuedAction
		var job, next string
		if err := rows.Scan(&job, &q.Key, &q.Kind, &q.Payload, &q.Delivered, &q.Attempts, &q.LastError, &next, &q.Failed); err != nil {
			return nil, err
		}
		q.Job = domain.JobID(job)
		if next != "" {
			if q.NextAttempt, err = time.Parse(time.RFC3339Nano, next); err != nil {
				return nil, fmt.Errorf("next attempt of action %q: %w", q.Key, err)
			}
		}
		qs = append(qs, q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return qs, nil
}

// MarkActionDelivered implements ports.Outbox.
func (s *Store) MarkActionDelivered(ctx context.Context, key string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE outbox SET delivered = 1, next_attempt = '' WHERE key = ?`, key)
	return marked(res, err, "delivered", key)
}

// MarkActionFailed implements ports.Outbox.
func (s *Store) MarkActionFailed(ctx context.Context, key string, f ports.ActionFailure) error {
	next := ""
	if !f.Failed && !f.NextAttempt.IsZero() {
		next = f.NextAttempt.Format(time.RFC3339Nano)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE outbox SET attempts = ?, last_error = ?, next_attempt = ?, failed = ? WHERE key = ?`,
		f.Attempts, f.LastError, next, f.Failed, key)
	return marked(res, err, "failed", key)
}

// marked reports an update that touched no row as ErrActionNotFound.
func marked(res sql.Result, err error, what, key string) error {
	if err != nil {
		return fmt.Errorf("sqlite: mark action %q %s: %w", key, what, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: mark action %q %s: %w", key, what, err)
	} else if n == 0 {
		return fmt.Errorf("sqlite: action %q: %w", key, ports.ErrActionNotFound)
	}
	return nil
}
