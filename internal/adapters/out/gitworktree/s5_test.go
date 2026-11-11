//go:build unix

package gitworktree_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/gitworktree"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/platform/childenv"
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

		// factoryd serve takes the repo as "." by default, and git drops a
		// relative GIT_CEILING_DIRECTORIES entry.
		s.It("fails, and saves nothing, when the worktree's .git is deleted and the repo is given as .", func(ctx *specs.Context) {
			h := relative(ctx, newHostile(ctx))
			wt := h.prepare(ctx)
			dirty(ctx, wt)
			ctx.Expect(os.Remove(filepath.Join(wt, ".git"))).To(specs.BeNil())

			err := h.ws.Clean(context.Background(), hostileJob)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("not a git repository"))
			_, err = run(h.repo, "rev-parse", "--verify", "--quiet", backupRef)
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})

		s.It("backs up the worktree's own files when the repo is given as .", func(ctx *specs.Context) {
			h := relative(ctx, newHostile(ctx))
			dirty(ctx, h.prepare(ctx))

			err := h.ws.Clean(context.Background(), hostileJob)

			ctx.Expect(err).To(specs.BeNil())
			files := must(ctx, h.repo, "--no-replace-objects", "ls-tree", "-r", "--name-only", backupRef)
			ctx.Expect(files).ToEqual(".gitattributes\ndata.txt\nnew.txt")
		})
	})
}

// relative is h with its Workspace over the repo given as ".", run from the
// repo, as factoryd serve runs by default.
func relative(ctx *specs.Context, h hostile) hostile {
	ctx.Testing().Chdir(h.repo)
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	h.ws = gitworktree.New(".", base, store, childenv.Homes{Root: filepath.Dir(h.home)})
	return h
}
