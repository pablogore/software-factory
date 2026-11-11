package spec

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// jobs is what the specgate run needs of ports.JobStore.
type jobs interface {
	Load(ctx context.Context, id domain.JobID) (domain.Job, ports.Version, error)
	Apply(ctx context.Context, id domain.JobID, v ports.Version, p domain.Pipeline, ev domain.Event, acts ...ports.Action) ([]domain.Event, ports.Version, error)
}

// history is what the specgate run needs of ports.EventLog: the spec the
// job's spec role handed in, as SpecWritten saved it.
type history interface {
	History(ctx context.Context, id domain.JobID) ([]ports.Record, error)
}

// pipelines finds the pipeline a job follows by its name.
type pipelines interface {
	Pipeline(ctx context.Context, name string) (domain.Pipeline, error)
}
