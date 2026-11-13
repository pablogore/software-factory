//go:build unix

package gitdiff_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestDemoS6(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ on a worktree whose path has a \":\"", func(s *specs.Spec) {
		bg := context.Background()

		// colon is a repo as newRepo makes it, moved under a folder named
		// "a:b", with its job branch's commit.
		colon := func(ctx *specs.Context) (string, string) {
			dir, fork := newRepo(ctx)
			moved := filepath.Join(ctx.Testing().TempDir(), "a:b")
			ctx.Expect(os.Rename(dir, moved)).To(specs.BeNil())
			return moved, fork
		}

		// expectRefused is err as run returns it for a path with a ":",
		// before running git.
		expectRefused := func(ctx *specs.Context, err error) {
			ctx.Testing().Helper()
			ctx.Expect(errors.Is(err, domain.ErrInvalidValue)).To(specs.BeTrue())
			ctx.Expect(err.Error()).To(specs.Contain(`has a ":"`))
		}

		s.It("fails every gate read with an error that says so", func(ctx *specs.Context) {
			dir, fork := colon(ctx)
			d := differ(ctx)

			changes, err := d.Changes(bg, job, dir, fork)
			expectRefused(ctx, err)
			ctx.Expect(changes).To(specs.BeEmpty())
			_, err = d.Lines(bg, job, dir, fork)
			expectRefused(ctx, err)
			flagged, err := d.Flagged(bg, job, dir)
			expectRefused(ctx, err)
			ctx.Expect(flagged).To(specs.BeEmpty())
			_, err = d.AtBase(bg, job, dir, fork, "README.md")
			expectRefused(ctx, err)
			_, _, err = d.AtBaseIfAny(bg, job, dir, fork, "README.md")
			expectRefused(ctx, err)
			_, err = d.InWorktree(bg, job, dir, "README.md")
			expectRefused(ctx, err)
		})

		// git splits GIT_CEILING_DIRECTORIES at ":": without the refusal, the
		// ceiling over .../a:b/.worktrees is gone and git reads the main
		// checkout's index as the worktree's.
		s.It("fails to list the flagged files when the worktree's .git is deleted, instead of reading the main checkout's", func(ctx *specs.Context) {
			main, fork := colon(ctx)
			wt := filepath.Join(main, ".worktrees", "job-1")
			must(ctx, main, "worktree", "add", "-q", "-b", "factory/wt", wt, fork)
			must(ctx, main, "update-index", "--skip-worktree", "README.md")
			ctx.Expect(os.Remove(filepath.Join(wt, ".git"))).To(specs.BeNil())
			// The premise: git, left to find the repo, reads the main checkout's index.
			ctx.Expect(must(ctx, wt, "ls-files", "-v", "--full-name", "--", ":/README.md")).ToEqual("S README.md")

			flagged, err := differ(ctx).Flagged(bg, job, wt)

			expectRefused(ctx, err)
			ctx.Expect(flagged).To(specs.BeEmpty())
		})

		s.It("reads a worktree on a plain path as before", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "README.md", "changed\n")
			d := differ(ctx)

			changes, err := d.Changes(bg, job, dir, fork)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "README.md", Kind: domain.ChangeModified}})
			lines, err := d.Lines(bg, job, dir, fork)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(lines).ToEqual(2)
			flagged, err := d.Flagged(bg, job, dir)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(flagged).To(specs.BeEmpty())
			content, err := d.AtBase(bg, job, dir, fork, "README.md")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(content).ToEqual("README.md\n")
			_, ok, err := d.AtBaseIfAny(bg, job, dir, fork, "README.md")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(ok).To(specs.BeTrue())
			got, err := d.InWorktree(bg, job, dir, "README.md")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual("changed\n")
		})
	})
}
