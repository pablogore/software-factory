package wiring

import (
	"context"
	"net/http"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

// Pipelines finds the pipeline a job follows by its name.
type Pipelines interface {
	Pipeline(ctx context.Context, name string) (domain.Pipeline, error)
}

// SystemClock tells the time from the system clock.
type SystemClock struct{}

// Now implements ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// WorkerAPI is the worker HTTP API over jobs, deciding results with the
// tokens issued from tokens.
func WorkerAPI(tokens *jobtoken.Tokens, jobs ports.JobStore, p Pipelines, clock ports.Clock) http.Handler {
	return mcpworker.Handler(&worker.Submitter{Tokens: tokens, Jobs: jobs, Pipelines: p, Clock: clock})
}
