package ticket

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// jobs is what the slice needs of ports.JobStore.
type jobs interface {
	// Create saves job and acts in one transaction, or nothing with
	// ports.ErrJobExists when the job is saved already.
	Create(ctx context.Context, job domain.Job, acts ...ports.Action) error
}

// discards is what the slice needs of ports.DiscardLog.
type discards interface {
	Discard(ctx context.Context, d ports.Discarded) error
}
