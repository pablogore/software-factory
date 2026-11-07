package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/pablogore/software-factory/internal/ports"
)

// Discard implements ports.DiscardLog.
func (s *Store) Discard(ctx context.Context, d ports.Discarded) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO discarded (provider, thread, request, author, at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (provider, thread, request) DO NOTHING`,
		d.Provider, d.Thread, d.Request, d.Author, d.At.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("sqlite: discard request %q of %s: %w", d.Request, d.Provider, err)
	}
	return nil
}

// Discards implements ports.DiscardLog.
func (s *Store) Discards(ctx context.Context) ([]ports.Discarded, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider, thread, request, author, at FROM discarded ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: discards: %w", err)
	}
	defer func() { _ = rows.Close() }() // rows.Err reports what matters
	ds := []ports.Discarded{}
	for rows.Next() {
		var d ports.Discarded
		var at string
		if err := rows.Scan(&d.Provider, &d.Thread, &d.Request, &d.Author, &at); err != nil {
			return nil, fmt.Errorf("sqlite: discards: %w", err)
		}
		if d.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("sqlite: time of discarded request %q: %w", d.Request, err)
		}
		ds = append(ds, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: discards: %w", err)
	}
	return ds, nil
}
