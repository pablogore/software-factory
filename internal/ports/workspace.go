package ports

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
)

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
	// base and other jobs alone. Uncommitted changes are first saved in a
	// backup commit that outlives the branch; if saving them fails nothing
	// is removed and the error is returned.
	Clean(ctx context.Context, job domain.JobID) error
}
