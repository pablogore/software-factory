// Package gitworktree implements ports.Workspace with git worktrees: each
// job gets .worktrees/<job-id> in the repo, on its own branch factory/<job-id>.
// Making a job's worktree records the commit the base was at in a
// ports.BaseLog, so the gate judges the job against that commit whatever
// happens later to the base's ref.
//
// Cleaning a job whose worktree has uncommitted changes first saves them, new
// files included, in a backup commit on top of the branch's head, kept at
// refs/factory-backup/<job-id> so it outlives the branch. To see it and get
// it back:
//
//	git show refs/factory-backup/<job-id>
//	git switch -c <branch> refs/factory-backup/<job-id>
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
	repo  string
	base  string
	bases ports.BaseLog
}

var _ ports.Workspace = (*Workspace)(nil)

// New returns a Workspace over the git repo at repo whose job branches start
// from base, recording in bases the commit each one starts from.
func New(repo, base string, bases ports.BaseLog) *Workspace {
	return &Workspace{repo: repo, base: base, bases: bases}
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
	commit, err := w.baseCommit(ctx, job)
	if err != nil {
		return ports.Worktree{}, err
	}
	if _, err := git(ctx, w.repo, "worktree", "add", "-b", wt.Branch, wt.Path, commit); err != nil {
		return ports.Worktree{}, err
	}
	return wt, nil
}

// baseCommit is the commit job's worktree is made from: the one recorded for
// it, when a try that recorded it failed to make the worktree or the worktree
// was cleaned, or else the commit base is at now, recorded before the
// worktree exists so no worktree is ever without one.
func (w *Workspace) baseCommit(ctx context.Context, job domain.JobID) (string, error) {
	commit, err := w.bases.Base(ctx, job)
	if err == nil || !errors.Is(err, ports.ErrNoBase) {
		return commit, err
	}
	if commit, err = git(ctx, w.repo, "rev-parse", "--verify", "--end-of-options", w.base+"^{commit}"); err != nil {
		return "", err
	}
	if err := w.bases.RecordBase(ctx, job, commit); err != nil {
		return "", err
	}
	return commit, nil
}

// Clean implements ports.Workspace.
func (w *Workspace) Clean(ctx context.Context, job domain.JobID) error {
	wt, err := w.worktree(job)
	if err != nil {
		return err
	}
	if err := w.backup(ctx, wt); err != nil {
		return fmt.Errorf("cleaning %s: %w", wt.Path, err)
	}
	// --force: the uncommitted changes it would refuse over are in the backup.
	if _, err := git(ctx, w.repo, "worktree", "remove", "--force", wt.Path); err != nil {
		return err
	}
	// -D: the branch's commits need not be merged into the base to go.
	_, err = git(ctx, w.repo, "branch", "-D", wt.Branch)
	return err
}

// backup saves the worktree's uncommitted changes, untracked files included,
// in a commit on top of HEAD at refs/factory-backup/<job-id>. A clean worktree
// gets no backup. It stages into a scratch index, so a failure leaves the
// worktree and its index as they were.
func (w *Workspace) backup(ctx context.Context, wt ports.Worktree) error {
	status, err := git(ctx, wt.Path, "status", "--porcelain")
	if err != nil || status == "" {
		return err
	}
	scratch, err := os.MkdirTemp("", "gitworktree-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := gitEnv(ctx, wt.Path, index, "read-tree", "HEAD"); err != nil {
		return err
	}
	if _, err := gitEnv(ctx, wt.Path, index, "add", "--all"); err != nil {
		return err
	}
	tree, err := gitEnv(ctx, wt.Path, index, "write-tree")
	if err != nil {
		return err
	}
	// A fixed identity, so a machine without git's user.name and user.email
	// can still save the backup.
	commit, err := git(ctx, wt.Path, "-c", "user.name=factoryd", "-c", "user.email=factoryd@localhost",
		"commit-tree", tree, "-p", "HEAD", "-m", "backup of uncommitted changes in "+wt.Branch)
	if err != nil {
		return err
	}
	_, err = git(ctx, wt.Path, "update-ref", backupRef(wt.Job), commit)
	return err
}

func backupRef(job domain.JobID) string {
	return "refs/factory-backup/" + string(job)
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
	return gitEnv(ctx, dir, nil, args...)
}

// gitEnv is git with env added to this process's environment. It ignores
// refs/replace/, which any worktree can write, so a replacement left by a job
// never changes which commit a new job's worktree is made from.
func gitEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects"}, args...)...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
