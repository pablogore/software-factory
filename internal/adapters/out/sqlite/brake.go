package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SaveBrake implements ports.BrakeLog: the table holds at most one row.
func (s *Store) SaveBrake(ctx context.Context, on bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO brake (id, engaged) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET engaged = excluded.engaged`, on)
	if err != nil {
		return fmt.Errorf("sqlite: save brake: %w", err)
	}
	return nil
}

// BrakeOn implements ports.BrakeLog.
func (s *Store) BrakeOn(ctx context.Context) (bool, error) {
	var on bool
	err := s.db.QueryRowContext(ctx, `SELECT engaged FROM brake WHERE id = 1`).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: read brake: %w", err)
	}
	return on, nil
}
