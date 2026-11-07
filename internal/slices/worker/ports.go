package worker

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

type tokens interface {
	Authorize(token string, op jobtoken.Op, job domain.JobID) (jobtoken.Grant, error)
	Revoke(token string)
}

type jobs interface {
	Load(ctx context.Context, id domain.JobID) (domain.Job, ports.Version, error)
	Apply(ctx context.Context, id domain.JobID, v ports.Version, p domain.Pipeline, ev domain.Event, acts ...ports.Action) ([]domain.Event, ports.Version, error)
	History(ctx context.Context, id domain.JobID) ([]ports.Record, error)
}

// pipelines finds the pipeline a job follows by its name.
type pipelines interface {
	Pipeline(ctx context.Context, name string) (domain.Pipeline, error)
}
