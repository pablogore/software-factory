package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/pablogore/software-factory/internal/domain"
)

// Plant writes job and evs to the database as they are, past Decide and
// Validate, as memstore.Plant does, so the contract can read back rows no
// Apply would write. It keeps the job's version, or starts it at 1.
func (s *Store) Plant(ctx context.Context, job domain.Job, evs ...domain.Event) (err error) {
	state, err := json.Marshal(job)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs (id, state, version) VALUES (?, ?, 1)
		ON CONFLICT (id) DO UPDATE SET state = excluded.state`, string(job.ID), string(state))
	if err != nil {
		return err
	}
	for _, ev := range evs {
		if err := insert(ctx, tx, job.ID, ev); err != nil {
			return err
		}
	}
	return tx.Commit()
}
