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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pablogore/software-factory/internal/adapters/out/gitsafe"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/ports"
)

// Workspace makes and removes job worktrees in one repo, with git run as
// gitsafe runs it and each job's HOME in homes: the repo's config, hooks and
// .gitattributes are shared with every job's worktree, so a builder could
// have git run its commands, which must get no token factoryd holds.
type Workspace struct {
	repo  string
	base  string
	bases ports.BaseLog
	homes childenv.Homes
}

var _ ports.Workspace = (*Workspace)(nil)

// New returns a Workspace over the git repo at repo whose job branches start
// from base, recording in bases the commit each one starts from, with git run
// for each job with its HOME in homes.
func New(repo, base string, bases ports.BaseLog, homes childenv.Homes) *Workspace {
	return &Workspace{repo: repo, base: base, bases: bases, homes: homes}
}

// Prepare implements ports.Workspace.
func (w *Workspace) Prepare(ctx context.Context, job domain.JobID) (ports.Worktree, error) {
	wt, err := w.worktree(job)
	if err != nil {
		return ports.Worktree{}, err
	}
	if _, err := os.Stat(wt.Path); err == nil {
		// Reusing it as it is keeps work a previous run left there. The
		// ceiling keeps git from reading the main checkout's branch when the
		// worktree's .git is gone.
		head, err := w.gitEnv(ctx, job, wt.Path, []string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(wt.Path)}, "rev-parse", "--abbrev-ref", "HEAD")
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
	if _, err := w.git(ctx, job, w.repo, "worktree", "add", "-b", wt.Branch, wt.Path, commit); err != nil {
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
	if commit, err = w.git(ctx, job, w.repo, "rev-parse", "--verify", "--end-of-options", w.base+"^{commit}"); err != nil {
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
	if _, err := w.git(ctx, job, w.repo, "worktree", "remove", "--force", wt.Path); err != nil {
		return err
	}
	// -D: the branch's commits need not be merged into the base to go.
	_, err = w.git(ctx, job, w.repo, "branch", "-D", wt.Branch)
	return err
}

// backup saves the worktree's uncommitted changes, untracked files included,
// in a commit on top of HEAD at refs/factory-backup/<job-id>. A clean worktree
// gets no backup. It stages into a scratch index, so a failure leaves the
// worktree and its index as they were. A submodule's own changes count for
// none: git add can't save them, and to tell them git status would run git
// in the submodule, with the submodule's config. git runs with the
// worktree's git folder and the worktree itself given: core.worktree, in the
// config or in config.worktree, is the builder's to set, and would have git
// status and git add take another folder's files as the worktree's, and
// save them where every job can read them.
func (w *Workspace) backup(ctx context.Context, wt ports.Worktree) error {
	// The ceiling keeps git from finding the main checkout's git folder when
	// the worktree's .git is gone.
	gitDir, err := w.gitEnv(ctx, wt.Job, wt.Path, []string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(wt.Path)}, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	at := []string{"--git-dir=" + gitDir, "--work-tree=" + wt.Path}
	status, err := w.git(ctx, wt.Job, wt.Path, append(at, "status", "--porcelain", "--ignore-submodules=dirty")...)
	if err != nil || status == "" {
		return err
	}
	scratch, err := os.MkdirTemp("", "gitworktree-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := w.gitEnv(ctx, wt.Job, wt.Path, index, append(at, "read-tree", "HEAD")...); err != nil {
		return err
	}
	if _, err := w.gitEnv(ctx, wt.Job, wt.Path, index, append(at, "add", "--all")...); err != nil {
		return err
	}
	tree, err := w.gitEnv(ctx, wt.Job, wt.Path, index, append(at, "write-tree")...)
	if err != nil {
		return err
	}
	// A fixed identity, so a machine without git's user.name and user.email
	// can still save the backup.
	commit, err := w.git(ctx, wt.Job, wt.Path, append(at, "-c", "user.name=factoryd", "-c", "user.email=factoryd@localhost",
		"commit-tree", tree, "-p", "HEAD", "-m", "backup of uncommitted changes in "+wt.Branch)...)
	if err != nil {
		return err
	}
	_, err = w.git(ctx, wt.Job, wt.Path, append(at, "update-ref", backupRef(wt.Job), commit)...)
	return err
}

func backupRef(job domain.JobID) string {
	return "refs/factory-backup/" + string(job)
}

// worktree names the job's path and branch, refusing ids that would land
// outside .worktrees. The path is absolute whatever the repo was given as:
// git runs in other folders than the one the repo is relative to, and drops
// a relative GIT_CEILING_DIRECTORIES entry. A path with a ":" is refused:
// git splits GIT_CEILING_DIRECTORIES at ":", and the halves would leave no
// ceiling over the worktree.
func (w *Workspace) worktree(job domain.JobID) (ports.Worktree, error) {
	id := string(job)
	if id == "" {
		return ports.Worktree{}, fmt.Errorf("job id: %w", domain.ErrMissingField)
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return ports.Worktree{}, fmt.Errorf("job id %q: %w", id, domain.ErrInvalidValue)
	}
	path, err := filepath.Abs(filepath.Join(w.repo, ".worktrees", id))
	if err != nil {
		return ports.Worktree{}, err
	}
	if strings.Contains(path, ":") {
		return ports.Worktree{}, fmt.Errorf("worktree path %s has a \":\", which GIT_CEILING_DIRECTORIES can't hold: %w", path, domain.ErrInvalidValue)
	}
	return ports.Worktree{
		Job:    job,
		Path:   path,
		Branch: "factory/" + id,
	}, nil
}

// git runs git in dir for job and returns its trimmed output; a failure
// carries git's own message.
func (w *Workspace) git(ctx context.Context, job domain.JobID, dir string, args ...string) (string, error) {
	return w.gitEnv(ctx, job, dir, nil, args...)
}

// gitEnv is git with env added, run as gitsafe runs it with job's HOME.
func (w *Workspace) gitEnv(ctx context.Context, job domain.JobID, dir string, env []string, args ...string) (string, error) {
	home, err := w.homes.For(job)
	if err != nil {
		return "", fmt.Errorf("gitworktree: %w", err)
	}
	out, err := gitsafe.Run(ctx, home, dir, env, args...)
	return strings.TrimSpace(out), err
}
