package gitdiff

import (
	"context"
	"fmt"
	"os"

	"github.com/pablogore/software-factory/internal/domain"
)

// Checkout checks out commit, a full object name, detached in a new
// directory of its own, from the repo the worktree at dir is in, and returns
// its path, with git run for job as Changes runs it. Hooks don't run: the
// hooks directory is shared with the job's worktree, so the builder could
// have put one there to change the copy.
func (d Differ) Checkout(ctx context.Context, job domain.JobID, dir, commit string) (string, error) {
	if !objectName(commit) {
		return "", fmt.Errorf("gitdiff: commit %q is not a full commit hash: %w", commit, domain.ErrInvalidValue)
	}
	home, err := d.home(job)
	if err != nil {
		return "", err
	}
	path, err := os.MkdirTemp("", "gitdiff-base-")
	if err != nil {
		return "", fmt.Errorf("gitdiff: %w", err)
	}
	if _, err := git(ctx, home, dir, "worktree", "add", "-q", "--detach", path, commit+"^{commit}"); err != nil {
		_ = os.RemoveAll(path)
		return "", err
	}
	return path, nil
}

// Remove removes the copy at path that Checkout made from the repo of the
// worktree at dir, with whatever the count left in it, with git run for
// job as Checkout runs it.
func (d Differ) Remove(ctx context.Context, job domain.JobID, dir, path string) error {
	home, err := d.home(job)
	if err != nil {
		return err
	}
	_, err = git(ctx, home, dir, "worktree", "remove", "--force", path)
	return err
}
