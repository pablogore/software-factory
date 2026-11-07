// Package gitworktree implements ports.Workspace with git worktrees: each
// job gets .worktrees/<job-id> in the repo, on its own branch factory/<job-id>.
package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// Workspace makes and removes job worktrees in one repo.
type Workspace struct {
	repo string
	base string
}

var _ ports.Workspace = (*Workspace)(nil)

// New returns a Workspace over the git repo at repo whose job branches start
// from base.
func New(repo, base string) *Workspace {
	return &Workspace{repo: repo, base: base}
}

// Prepare implements ports.Workspace.
func (w *Workspace) Prepare(ctx context.Context, job domain.JobID) (ports.Worktree, error) {
	wt, err := w.worktree(job)
	if err != nil {
		return ports.Worktree{}, err
	}
	if _, err := os.Stat(wt.Path); err == nil {
		// Reusing it as it is keeps work a previous run left there.
		head, err := git(ctx, wt.Path, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return ports.Worktree{}, err
		}
		if head != wt.Branch {
			return ports.Worktree{}, fmt.Errorf("worktree %s is on %q, not %q: %w", wt.Path, head, wt.Branch, domain.ErrInvalidValue)
		}
		return wt, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ports.Worktree{}, err
	}
	if _, err := git(ctx, w.repo, "worktree", "add", "-b", wt.Branch, wt.Path, w.base); err != nil {
		return ports.Worktree{}, err
	}
	return wt, nil
}

// Clean implements ports.Workspace.
func (w *Workspace) Clean(ctx context.Context, job domain.JobID) error {
	wt, err := w.worktree(job)
	if err != nil {
		return err
	}
	status, err := git(ctx, wt.Path, "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("cleaning %s: %w:\n%s", wt.Path, ports.ErrUncommittedChanges, status)
	}
	if _, err := git(ctx, w.repo, "worktree", "remove", wt.Path); err != nil {
		return err
	}
	// -D: the branch's commits need not be merged into the base to go.
	_, err = git(ctx, w.repo, "branch", "-D", wt.Branch)
	return err
}

// worktree names the job's path and branch, refusing ids that would land
// outside .worktrees.
func (w *Workspace) worktree(job domain.JobID) (ports.Worktree, error) {
	id := string(job)
	if id == "" {
		return ports.Worktree{}, fmt.Errorf("job id: %w", domain.ErrMissingField)
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return ports.Worktree{}, fmt.Errorf("job id %q: %w", id, domain.ErrInvalidValue)
	}
	return ports.Worktree{
		Job:    job,
		Path:   filepath.Join(w.repo, ".worktrees", id),
		Branch: "factory/" + id,
	}, nil
}

// git runs git in dir and returns its trimmed output; a failure carries
// git's own message.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
