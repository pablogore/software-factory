package worker

import (
	"context"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
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

// session is the agent session an attempt prompts, and whose last stderr
// lines a failed attempt cites.
type session interface {
	Prompt(ctx context.Context, msg string) (<-chan domain.AgentProgressed, error)
	Stderr() []string
}

// results tells what a token's submissions came to.
type results interface {
	Take(token string) Outcome
}

// pipelines finds the pipeline a job follows by its name.
type pipelines interface {
	Pipeline(ctx context.Context, name string) (domain.Pipeline, error)
}

// gate runs a job's deterministic checks in its worktree, as the gate does,
// without deciding anything on the job.
type gate interface {
	Check(ctx context.Context, id domain.JobID) (domain.ChecksRun, error)
}

// locks is the per-job lock a job's checks run under, shared with the gate:
// a run_gate run takes it with Try, and a submitted result cancels that run
// with Preempt, waiting until it stopped.
type locks interface {
	Try(ctx context.Context, job domain.JobID) (context.Context, func(), error)
	Preempt(ctx context.Context, job domain.JobID) error
}

// devServers runs the dev server of a role's session in its job's worktree,
// keyed by job and stage as ports.DevServers is.
type devServers interface {
	Start(ctx context.Context, wt ports.Worktree, stage string, port int, cmd ports.DevCommand) error
	StopStage(ctx context.Context, job domain.JobID, stage string) error
}

// worktrees gives a job's worktree, the one its session works in.
type worktrees interface {
	Prepare(ctx context.Context, job domain.JobID) (ports.Worktree, error)
}

// devPorts hands out a port of job's own range that is not in taken and
// that nothing listens on.
type devPorts interface {
	Free(job domain.JobID, taken []int) (int, error)
}

// devCommands tells the dev server command the repo configures for a job,
// or none.
type devCommands interface {
	DevCommand(ctx context.Context, job domain.JobID, wt ports.Worktree) (ports.DevCommand, error)
}

// issuer issues a session's token and revokes it when the session ends.
type issuer interface {
	Issue(g jobtoken.Grant) (string, error)
	Revoke(token string)
}

// roles reads a role and its prompt as its session starts.
type roles interface {
	Role(ctx context.Context, name string) (domain.Role, string, error)
}

// agents starts a session of the agent role names.
type agents interface {
	Start(ctx context.Context, role domain.Role, s ports.SessionSpec) (ports.Session, error)
}

// slots runs work for a job when a slot is free and the job may spend.
type slots interface {
	Submit(ctx context.Context, job domain.JobID, w scheduler.Work) <-chan error
}

// timer tells when a duration passed.
type timer interface {
	After(d time.Duration) <-chan time.Time
}
