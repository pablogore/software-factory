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

// WorkerStore keeps jobs and reads back their history, which tells whether a
// token's stage entry is still the job's.
type WorkerStore interface {
	ports.JobStore
	ports.EventLog
}

// GateChecks runs a job's checks without deciding anything on it, as
// build.Gate's Check does.
type GateChecks interface {
	Check(ctx context.Context, id domain.JobID) (domain.ChecksRun, error)
}

// WorkerAPI is the worker HTTP API over jobs, deciding results with the
// tokens issued from tokens and running a builder's checks with gate.
func WorkerAPI(tokens *jobtoken.Tokens, jobs WorkerStore, p Pipelines, gate GateChecks, clock ports.Clock) http.Handler {
	h, _ := Worker(tokens, jobs, p, gate, clock)
	return h
}

// Worker is WorkerAPI and the Attempts that run agents' turns over the same
// submissions, so an attempt sees the results its agent handed in through
// the API.
func Worker(tokens *jobtoken.Tokens, jobs WorkerStore, p Pipelines, gate GateChecks, clock ports.Clock) (http.Handler, *worker.Attempts) {
	sub := &worker.Submitter{Tokens: tokens, Jobs: jobs, Pipelines: p, Gate: gate, Clock: clock}
	return mcpworker.Handler(sub), &worker.Attempts{Results: sub, Jobs: jobs, Pipelines: p, Clock: clock}
}
