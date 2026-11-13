//go:build unix

package gitworktree_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

func TestDemoS6(t *testing.T) {
	specs.Describe(t, "gitworktree.Workspace preparing a worktree again", func(s *specs.Spec) {
		bg := context.Background()
		const job domain.JobID = "job-1"

		// .worktrees/<id> sits in the main checkout: with no .git of its own,
		// git finds the main checkout's git folder and reads its branch.
		s.It("fails when the worktree's .git is deleted and the main checkout is on the job's branch", func(ctx *specs.Context) {
			repo := newRepo(ctx)
			ws, _ := newWorkspace(ctx, repo)
			wt, err := ws.Prepare(bg, job)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(os.Remove(filepath.Join(wt.Path, ".git"))).To(specs.BeNil())
			must(ctx, repo, "worktree", "prune")
			must(ctx, repo, "switch", "-q", "factory/job-1")
			// The premise: git, left to find the repo, reads the main checkout's branch.
			ctx.Expect(must(ctx, wt.Path, "rev-parse", "--abbrev-ref", "HEAD")).ToEqual("factory/job-1")

			_, err = ws.Prepare(bg, job)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("not a git repository"))
		})

		s.It("refuses a repo whose path has a \":\" before running git", func(ctx *specs.Context) {
			repo := filepath.Join(ctx.Testing().TempDir(), "a:b")
			ctx.Expect(os.Rename(newRepo(ctx), repo)).To(specs.BeNil())
			ws, store := newWorkspace(ctx, repo)

			_, err := ws.Prepare(bg, job)

			ctx.Expect(errors.Is(err, domain.ErrInvalidValue)).To(specs.BeTrue())
			ctx.Expect(err.Error()).To(specs.Contain(`has a ":"`))
			ctx.Expect(exists(filepath.Join(repo, ".worktrees"))).To(specs.BeFalse())
			ctx.Expect(hasBranch(repo, "factory/job-1")).To(specs.BeFalse())
			_, err = store.Base(bg, job)
			ctx.Expect(errors.Is(err, ports.ErrNoBase)).To(specs.BeTrue())

			err = ws.Clean(bg, job)
			ctx.Expect(errors.Is(err, domain.ErrInvalidValue)).To(specs.BeTrue())
			ctx.Expect(err.Error()).To(specs.Contain(`has a ":"`))
		})

		// git splits GIT_CEILING_DIRECTORIES at ":": without the refusal, the
		// ceiling over a.../a:b/.worktrees is gone and git reads the main
		// checkout's branch.
		s.It("refuses a repo whose path has a \":\" when the worktree's .git is deleted and the main checkout is on the job's branch", func(ctx *specs.Context) {
			repo := filepath.Join(ctx.Testing().TempDir(), "a:b")
			ctx.Expect(os.Rename(newRepo(ctx), repo)).To(specs.BeNil())
			ws, _ := newWorkspace(ctx, repo)
			wt := filepath.Join(repo, ".worktrees", "job-1")
			must(ctx, repo, "worktree", "add", "-q", "-b", "factory/job-1", wt, base)
			ctx.Expect(os.Remove(filepath.Join(wt, ".git"))).To(specs.BeNil())
			must(ctx, repo, "worktree", "prune")
			must(ctx, repo, "switch", "-q", "factory/job-1")

			_, err := ws.Prepare(bg, job)

			ctx.Expect(errors.Is(err, domain.ErrInvalidValue)).To(specs.BeTrue())
			ctx.Expect(err.Error()).To(specs.Contain(`has a ":"`))
		})

		s.It("refuses a job whose worktree path has a \":\" before running git", func(ctx *specs.Context) {
			repo := newRepo(ctx)
			ws, store := newWorkspace(ctx, repo)

			_, err := ws.Prepare(bg, "job:1")

			ctx.Expect(errors.Is(err, domain.ErrInvalidValue)).To(specs.BeTrue())
			ctx.Expect(err.Error()).To(specs.Contain(`has a ":"`))
			ctx.Expect(hasBranch(repo, "factory/job:1")).To(specs.BeFalse())
			_, err = store.Base(bg, "job:1")
			ctx.Expect(errors.Is(err, ports.ErrNoBase)).To(specs.BeTrue())
		})

		s.It("reuses an intact worktree on a plain path, as before", func(ctx *specs.Context) {
			repo := newRepo(ctx)
			ws, _ := newWorkspace(ctx, repo)
			first, err := ws.Prepare(bg, job)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(first.Path, "work.txt"), []byte("work\n"), 0o600)).To(specs.BeNil())

			again, err := ws.Prepare(bg, job)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(again).ToEqual(first)
			ctx.Expect(exists(filepath.Join(again.Path, "work.txt"))).To(specs.BeTrue())
		})
	})
}
