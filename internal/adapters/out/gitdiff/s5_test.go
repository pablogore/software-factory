package gitdiff_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

func TestDemoS5(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ on a worktree whose .git the builder deleted", func(s *specs.Spec) {
		bg := context.Background()

		// As factoryd lays it out: the worktree at .worktrees/<id> in the
		// main checkout, whose index flags a file the worktree's doesn't.
		deleted := func(ctx *specs.Context) (string, string) {
			main, fork := newRepo(ctx)
			wt := filepath.Join(main, ".worktrees", "job-1")
			must(ctx, main, "worktree", "add", "-q", "-b", "factory/wt", wt, fork)
			must(ctx, main, "update-index", "--skip-worktree", "README.md")
			ctx.Expect(os.Remove(filepath.Join(wt, ".git"))).To(specs.BeNil())
			// The premise: git, left to find the repo, reads the main checkout's index.
			ctx.Expect(must(ctx, wt, "ls-files", "-v", "--full-name", "--", ":/README.md")).ToEqual("S README.md")
			return wt, fork
		}

		s.It("fails to list the flagged files instead of reading the main checkout's", func(ctx *specs.Context) {
			wt, _ := deleted(ctx)

			flagged, err := differ(ctx).Flagged(bg, job, wt)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("not a git repository"))
			ctx.Expect(flagged).To(specs.BeEmpty())
		})

		s.It("fails to read a file at the base instead of reading it from the main checkout's repo", func(ctx *specs.Context) {
			wt, fork := deleted(ctx)
			d := differ(ctx)

			content, err := d.AtBase(bg, job, wt, fork, "README.md")
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("not a git repository"))
			ctx.Expect(content).ToEqual("")

			content, ok, err := d.AtBaseIfAny(bg, job, wt, fork, "README.md")
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("not a git repository"))
			ctx.Expect(ok).To(specs.BeFalse())
			ctx.Expect(content).ToEqual("")
		})
	})
}
