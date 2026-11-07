package ports

import (
	"context"
	"errors"

	"github.com/pablogore/software-factory/internal/domain"
)

// Base log errors. Callers compare with errors.Is; the wrapped message names
// the job.
var (
	// ErrNoBase means no base commit was recorded for the job: its worktree
	// was made before factoryd recorded them, or never made.
	ErrNoBase = errors.New("no base commit recorded")
	// ErrBaseRecorded means the job already has a different base commit,
	// which is kept.
	ErrBaseRecorded = errors.New("a different base commit is already recorded")
)

// BaseLog keeps the commit each job's worktree was made from. It lives in
// factoryd's store, not in git: refs are shared with the job's worktree, so
// a builder could move one, and the gate must judge the job's diff against
// a commit the builder cannot change.
type BaseLog interface {
	// RecordBase saves commit, a full object name, as job's base. Recording
	// the same commit again is not an error; a different one is
	// ErrBaseRecorded and the first stays.
	RecordBase(ctx context.Context, job domain.JobID, commit string) error
	// Base is the commit recorded for job, or ErrNoBase.
	Base(ctx context.Context, job domain.JobID) (string, error)
}
