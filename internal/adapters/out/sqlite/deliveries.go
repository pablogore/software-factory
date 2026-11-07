package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// inTx runs fn in one transaction, rolled back if fn fails.
func (s *Store) inTx(ctx context.Context, what string, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin %s: %w", what, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit %s: %w", what, err)
	}
	return nil
}

// jobOf is the job the event at seq belongs to. The store reads it from the
// event, so a delivery is never filed under the wrong job.
func jobOf(ctx context.Context, q querier, seq ports.Seq) (domain.JobID, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT job_id FROM events WHERE seq = ?`, int64(seq)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sqlite: event %d: %w", seq, ports.ErrEventNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: event %d: %w", seq, err)
	}
	return domain.JobID(id), nil
}

// MarkProcessed implements ports.EventFeed.
func (s *Store) MarkProcessed(ctx context.Context, subscriber string, seq ports.Seq) error {
	return s.inTx(ctx, "mark processed", func(tx *sql.Tx) error {
		return processed(ctx, tx, subscriber, seq)
	})
}

func processed(ctx context.Context, tx *sql.Tx, subscriber string, seq ports.Seq) error {
	job, err := jobOf(ctx, tx, seq)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO processed (subscriber, job_id, seq) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		subscriber, string(job), int64(seq)); err != nil {
		return fmt.Errorf("sqlite: mark event %d processed by %q: %w", seq, subscriber, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM deliveries WHERE subscriber = ? AND seq = ?`, subscriber, int64(seq)); err != nil {
		return fmt.Errorf("sqlite: clear delivery of event %d to %q: %w", seq, subscriber, err)
	}
	return nil
}

// MarkFailed implements ports.EventFeed.
func (s *Store) MarkFailed(ctx context.Context, subscriber string, seq ports.Seq, f ports.Failure) error {
	return s.inTx(ctx, "mark failed", func(tx *sql.Tx) error {
		job, err := jobOf(ctx, tx, seq)
		if err != nil {
			return err
		}
		if !f.Dead {
			_, err = tx.ExecContext(ctx, `INSERT INTO deliveries (subscriber, job_id, seq, attempts, last_error, next_attempt) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (subscriber, seq) DO UPDATE SET attempts = excluded.attempts, last_error = excluded.last_error, next_attempt = excluded.next_attempt`,
				subscriber, string(job), int64(seq), f.Attempts, f.LastError, f.NextAttempt.Format(time.RFC3339Nano))
			if err != nil {
				return fmt.Errorf("sqlite: save delivery of event %d to %q: %w", seq, subscriber, err)
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM deliveries WHERE subscriber = ? AND seq = ?`, subscriber, int64(seq)); err != nil {
			return fmt.Errorf("sqlite: clear delivery of event %d to %q: %w", seq, subscriber, err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO dead_events (subscriber, job_id, seq, attempts, last_error) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (subscriber, seq) DO UPDATE SET attempts = excluded.attempts, last_error = excluded.last_error`,
			subscriber, string(job), int64(seq), f.Attempts, f.LastError)
		if err != nil {
			return fmt.Errorf("sqlite: save dead event %d of %q: %w", seq, subscriber, err)
		}
		return nil
	})
}

// Deliveries implements ports.EventFeed.
func (s *Store) Deliveries(ctx context.Context, subscriber string) ([]ports.Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, job_id, attempts, last_error, next_attempt, 0 FROM deliveries WHERE subscriber = ?
		UNION ALL
		SELECT seq, job_id, attempts, last_error, '', 1 FROM dead_events WHERE subscriber = ?
		ORDER BY seq`, subscriber, subscriber)
	if err != nil {
		return nil, fmt.Errorf("sqlite: deliveries to %q: %w", subscriber, err)
	}
	defer func() { _ = rows.Close() }() // rows.Err reports what matters
	ds := []ports.Delivery{}
	for rows.Next() {
		d := ports.Delivery{Subscriber: subscriber}
		var job, next string
		if err := rows.Scan(&d.Seq, &job, &d.Attempts, &d.LastError, &next, &d.Dead); err != nil {
			return nil, fmt.Errorf("sqlite: deliveries to %q: %w", subscriber, err)
		}
		d.Job = domain.JobID(job)
		if !d.Dead {
			if d.NextAttempt, err = time.Parse(time.RFC3339Nano, next); err != nil {
				return nil, fmt.Errorf("sqlite: next attempt of event %d to %q: %w", d.Seq, subscriber, err)
			}
		}
		ds = append(ds, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: deliveries to %q: %w", subscriber, err)
	}
	return ds, nil
}

// RetryDead implements ports.DeadEvents.
func (s *Store) RetryDead(ctx context.Context, subscriber string, seq ports.Seq) error {
	return s.inTx(ctx, "retry dead event", func(tx *sql.Tx) error {
		return undead(ctx, tx, subscriber, seq)
	})
}

// DiscardDead implements ports.DeadEvents.
func (s *Store) DiscardDead(ctx context.Context, subscriber string, seq ports.Seq) error {
	return s.inTx(ctx, "discard dead event", func(tx *sql.Tx) error {
		if err := undead(ctx, tx, subscriber, seq); err != nil {
			return err
		}
		return processed(ctx, tx, subscriber, seq)
	})
}

// undead removes subscriber's dead event at seq, or fails with ErrNotDead.
func undead(ctx context.Context, tx *sql.Tx, subscriber string, seq ports.Seq) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM dead_events WHERE subscriber = ? AND seq = ?`, subscriber, int64(seq))
	if err != nil {
		return fmt.Errorf("sqlite: dead event %d of %q: %w", seq, subscriber, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("sqlite: dead event %d of %q: %w", seq, subscriber, err)
	} else if n == 0 {
		return fmt.Errorf("sqlite: event %d of %q: %w", seq, subscriber, ports.ErrNotDead)
	}
	return nil
}
