package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pablogore/software-factory/internal/ports"
)

// SaveIssueCaps implements ports.CapLog: one row per issue, its state as
// JSON.
func (s *Store) SaveIssueCaps(ctx context.Context, c ports.IssueCaps) error {
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("sqlite: save caps of issue #%d: %w", c.Issue, err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO issue_caps (issue, data) VALUES (?, ?) ON CONFLICT (issue) DO UPDATE SET data = excluded.data`, c.Issue, string(data))
	if err != nil {
		return fmt.Errorf("sqlite: save caps of issue #%d: %w", c.Issue, err)
	}
	return nil
}

// IssueCaps implements ports.CapLog.
func (s *Store) IssueCaps(ctx context.Context, n int) (ports.IssueCaps, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM issue_caps WHERE issue = ?`, n).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.IssueCaps{Issue: n}, nil
	}
	if err != nil {
		return ports.IssueCaps{}, fmt.Errorf("sqlite: read caps of issue #%d: %w", n, err)
	}
	var c ports.IssueCaps
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return ports.IssueCaps{}, fmt.Errorf("sqlite: read caps of issue #%d: %w", n, err)
	}
	return c, nil
}
