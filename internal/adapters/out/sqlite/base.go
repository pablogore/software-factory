package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// RecordBase implements ports.BaseLog: the insert keeps a base already
// recorded, which is then read back to tell a repeat from a different one.
func (s *Store) RecordBase(ctx context.Context, job domain.JobID, commit string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO bases (job_id, commit_name) VALUES (?, ?) ON CONFLICT (job_id) DO NOTHING`, string(job), commit)
	if err != nil {
		return fmt.Errorf("sqlite: record base of job %q: %w", job, err)
	}
	got, err := s.Base(ctx, job)
	if err != nil {
		return err
	}
	if got != commit {
		return fmt.Errorf("sqlite: base of job %q: %w: %s", job, ports.ErrBaseRecorded, got)
	}
	return nil
}

// Base implements ports.BaseLog.
func (s *Store) Base(ctx context.Context, job domain.JobID) (string, error) {
	var commit string
	err := s.db.QueryRowContext(ctx, `SELECT commit_name FROM bases WHERE job_id = ?`, string(job)).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sqlite: job %q: %w", job, ports.ErrNoBase)
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: base of job %q: %w", job, err)
	}
	return commit, nil
}
