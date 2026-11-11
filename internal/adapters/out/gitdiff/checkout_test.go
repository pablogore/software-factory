package gitdiff_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestCheckout(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ's checkout of the base", func(s *specs.Spec) {
		bg := context.Background()

		s.It("checks the base commit out apart from the worktree, whatever the worktree did, and removes it with what was left in it", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "README.md", "committed\n")
			must(ctx, dir, "commit", "-q", "-am", "job")
			write(ctx, dir, "README.md", "uncommitted\n")

			copyDir, err := differ(ctx).Checkout(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			readme, err := os.ReadFile(filepath.Join(copyDir, "README.md"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(string(readme)).ToEqual("README.md\n")
			write(ctx, copyDir, "build/out.txt", "left by the count\n")

			ctx.Expect(differ(ctx).Remove(bg, job, dir, copyDir)).To(specs.BeNil())

			_, err = os.Stat(copyDir)
			ctx.Expect(os.IsNotExist(err)).To(specs.BeTrue())
			ctx.Expect(must(ctx, dir, "worktree", "list", "--porcelain")).To(specs.Not(specs.Contain(copyDir)))
		})

		s.It("runs no hook the worktree could have put in the shared hooks directory", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
			ctx.Expect(os.WriteFile(hook, []byte("#!/bin/sh\necho hooked > hooked.txt\n"), 0o700)).To(specs.BeNil())

			copyDir, err := differ(ctx).Checkout(bg, job, dir, fork)
			ctx.Expect(err).To(specs.BeNil())
			defer func() { _ = differ(ctx).Remove(bg, job, dir, copyDir) }()

			_, err = os.Stat(filepath.Join(copyDir, "hooked.txt"))
			ctx.Expect(os.IsNotExist(err)).To(specs.BeTrue())
		})

		s.It("refuses a ref as the commit to check out", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := differ(ctx).Checkout(bg, job, dir, "HEAD")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}
