package build

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// jobs is what the gate needs of ports.JobStore.
type jobs interface {
	Load(ctx context.Context, id domain.JobID) (domain.Job, ports.Version, error)
	Apply(ctx context.Context, id domain.JobID, v ports.Version, p domain.Pipeline, ev domain.Event, acts ...ports.Action) ([]domain.Event, ports.Version, error)
}

// pipelines finds the pipeline a job follows by its name.
type pipelines interface {
	Pipeline(ctx context.Context, name string) (domain.Pipeline, error)
}

// worktrees is what the gate needs of ports.Workspace: the job's worktree,
// which its builder left in place.
type worktrees interface {
	Prepare(ctx context.Context, job domain.JobID) (ports.Worktree, error)
}

// checks runs a command in a directory and reports how it went. A command
// that ran and exited non-zero is a run, not an error; an error means it
// could not be run at all.
type checks interface {
	Run(ctx context.Context, dir string, command []string) (domain.ChecksRun, error)
}

// bases is what the gate needs of ports.BaseLog: the commit recorded when
// the job's worktree was made, or ports.ErrNoBase.
type bases interface {
	Base(ctx context.Context, job domain.JobID) (string, error)
}

// diffs lists the files a worktree changed against base, the commit its
// branch was made from, and the files its index marks so that no diff shows
// their changes.
type diffs interface {
	Changes(ctx context.Context, dir, base string) ([]domain.FileChange, error)
	Flagged(ctx context.Context, dir string) ([]domain.FlaggedFile, error)
}
