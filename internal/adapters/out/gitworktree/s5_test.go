//go:build unix

package gitworktree_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

func TestDemoS5(t *testing.T) {
	specs.Describe(t, "gitworktree.Workspace backing up a worktree whose core.worktree the builder set", func(s *specs.Spec) {
		// git takes core.worktree in the config every worktree shares only
		// for the main checkout, not for a linked worktree such as a job's.
		s.It("saves only the worktree's files, none of the folder core.worktree in its config.worktree names", func(ctx *specs.Context) {
			h := newHostile(ctx)
			wt := h.prepare(ctx)
			dirty(ctx, wt)
			outside := ctx.Testing().TempDir()
			write(ctx, outside, "secret.txt", "secret\n")
			h.config(ctx, "extensions.worktreeConfig", "true")
			must(ctx, wt, "config", "--worktree", "core.worktree", outside)
			// The premise: git, left to its config, takes outside as the worktree.
			ctx.Expect(must(ctx, wt, "status", "--porcelain")).To(specs.Contain("secret.txt"))

			err := h.ws.Clean(context.Background(), hostileJob)

			ctx.Expect(err).To(specs.BeNil())
			files := must(ctx, h.repo, "--no-replace-objects", "ls-tree", "-r", "--name-only", backupRef)
			ctx.Expect(files).ToEqual(".gitattributes\ndata.txt\nnew.txt")
			ctx.Expect(must(ctx, h.repo, "--no-replace-objects", "cat-file", "blob", backupRef+":data.txt")).ToEqual("changed")
			ctx.Expect(exists(filepath.Join(outside, "secret.txt"))).To(specs.BeTrue())
		})

		// .worktrees/<id> sits in the main checkout: with no .git of its own,
		// git finds the main checkout's git folder.
		s.It("fails, and saves nothing, when the worktree's .git is deleted", func(ctx *specs.Context) {
			h := newHostile(ctx)
			wt := h.prepare(ctx)
			dirty(ctx, wt)
			ctx.Expect(os.Remove(filepath.Join(wt, ".git"))).To(specs.BeNil())

			err := h.ws.Clean(context.Background(), hostileJob)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("not a git repository"))
			_, err = run(h.repo, "rev-parse", "--verify", "--quiet", backupRef)
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})
	})
}
