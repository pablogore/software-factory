package wiring

import (
	"context"
	"net/http"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
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

// After implements scheduler.Clock.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

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

// ContextPacks is where the worker's context_pack reads a job's base from:
// Bases, the commit recorded for it, which the pack's diff command is
// against.
type ContextPacks struct {
	Bases ports.BaseLog
}

// WorkerAPI is the worker HTTP API over jobs, deciding results with the
// tokens issued from tokens and running a builder's checks with gate under
// the job's lock in locks, the one the job's real gate takes: a result
// submitted cancels a run in progress. dev runs the sessions' dev servers;
// without it dev_server is refused. packs tells the jobs' bases for
// context_pack; without it context_pack is refused.
func WorkerAPI(tokens *jobtoken.Tokens, jobs WorkerStore, p Pipelines, gate GateChecks, locks *gatelock.Locks, clock ports.Clock, dev *DevServers, packs *ContextPacks) http.Handler {
	h, _ := Worker(tokens, jobs, p, gate, locks, clock, dev, packs)
	return h
}

// Worker is WorkerAPI and the Attempts that run agents' turns over the same
// submissions, so an attempt sees the results its agent handed in through
// the API.
func Worker(tokens *jobtoken.Tokens, jobs WorkerStore, p Pipelines, gate GateChecks, locks *gatelock.Locks, clock ports.Clock, dev *DevServers, packs *ContextPacks) (http.Handler, *worker.Attempts) {
	sub := &worker.Submitter{Tokens: tokens, Jobs: jobs, Pipelines: p, Gate: gate, Locks: locks, Clock: clock}
	if dev != nil {
		sub.Servers, sub.Worktrees, sub.Ports, sub.DevCommands = dev.Servers, dev.Worktrees, dev.Ports, baseDevCommand{dev.Bases}
		dev.sub = sub
	}
	if packs != nil {
		sub.Bases = packs.Bases
	}
	return mcpworker.Handler(sub), &worker.Attempts{Results: sub, Jobs: jobs, Pipelines: p, Clock: clock}
}
