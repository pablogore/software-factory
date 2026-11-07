package ports

import (
	"context"
	"errors"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrUncommittedChanges means a job's worktree has changes no commit holds,
// so cleaning it would lose them. Callers compare with errors.Is.
var ErrUncommittedChanges = errors.New("worktree has uncommitted changes")

// Worktree is a job's own checkout of the repo and the branch it works on.
type Worktree struct {
	Job    domain.JobID
	Path   string
	Branch string
}

// Workspace gives each job its own checkout, so jobs running at once never
// share files or a branch.
type Workspace interface {
	// Prepare makes the job's worktree on a new branch off the configured
	// base. When the job's worktree already exists it is reused as it is.
	Prepare(ctx context.Context, job domain.JobID) (Worktree, error)
	// Clean removes the job's worktree and its local branch, leaving the
	// base and other jobs alone. A worktree with uncommitted changes is
	// left as it is, with ErrUncommittedChanges.
	Clean(ctx context.Context, job domain.JobID) error
}
