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

// checks runs a command for a job in a directory and reports how it went;
// the job picks the environment it runs with, such as its HOME. A command
// that ran and exited non-zero is a run, not an error; an error means it
// could not be run at all.
type checks interface {
	Run(ctx context.Context, job domain.JobID, dir string, command []string) (domain.ChecksRun, error)
}

// bases is what the gate needs of ports.BaseLog: the commit recorded when
// the job's worktree was made, or ports.ErrNoBase.
type bases interface {
	Base(ctx context.Context, job domain.JobID) (string, error)
}

// diffs lists the files a worktree changed against base, the commit its
// branch was made from, and the files its index marks so that no diff shows
// their changes, and reads a file as base has it and as the worktree has it,
// by its path from the repo root; the job picks the environment git runs
// with, as it does the checks'.
type diffs interface {
	Changes(ctx context.Context, job domain.JobID, dir, base string) ([]domain.FileChange, error)
	Flagged(ctx context.Context, job domain.JobID, dir string) ([]domain.FlaggedFile, error)
	AtBase(ctx context.Context, job domain.JobID, dir, base, file string) (string, error)
	InWorktree(ctx context.Context, job domain.JobID, dir, file string) (string, error)
}

// snapshots checks out a commit, by its full object name, in a directory of
// its own apart from the worktree at dir, from the repo dir is in, and
// removes it, so the gate counts the base's tests on files the builder never
// had; the job picks the environment git runs with.
type snapshots interface {
	Checkout(ctx context.Context, job domain.JobID, dir, commit string) (string, error)
	Remove(ctx context.Context, job domain.JobID, dir, path string) error
}

// repoConfig is the test count command the repo configures at base, the
// full object name of a commit, read from the repo dir is in; none when it
// configures none; the job picks the environment git runs with.
type repoConfig interface {
	CountCommand(ctx context.Context, job domain.JobID, dir, base string) ([]string, error)
}

// locks is the per-job lock a job's checks run under, shared with a
// builder's run_gate: the gate takes it with Acquire, which cancels a
// run_gate run and waits until it stopped.
type locks interface {
	Acquire(ctx context.Context, job domain.JobID) (func(), error)
}
