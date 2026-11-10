package gitdiff_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/domain"
)

func TestContextPackPatch(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ's patch for a context pack", func(s *specs.Spec) {
		bg := context.Background()

		s.It("is empty for a branch that changed nothing", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)

			patch, err := gitdiff.Differ{}.Patch(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(patch).ToEqual("")
		})

		s.It("shows what the branch committed and what it left uncommitted, against the base", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "README.md", "README.md\nmore\n")
			must(ctx, dir, "commit", "-q", "-am", "readme")
			write(ctx, dir, "AGENTS.md", "changed\n")

			patch, err := gitdiff.Differ{}.Patch(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(patch).To(specs.Contain("diff --git a/AGENTS.md b/AGENTS.md\n"))
			ctx.Expect(patch).To(specs.Contain("-AGENTS.md\n+changed\n"))
			ctx.Expect(patch).To(specs.Contain("diff --git a/README.md b/README.md\n"))
			ctx.Expect(patch).To(specs.Contain(" README.md\n+more\n"))
		})

		s.It("never runs a diff driver or a textconv the worktree configures", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			marker := filepath.Join(ctx.Testing().TempDir(), "ran")
			must(ctx, dir, "config", "diff.evil.command", "touch "+marker)
			must(ctx, dir, "config", "diff.conv.textconv", "touch "+marker+"; cat")
			write(ctx, dir, ".gitattributes", "README.md diff=evil\nAGENTS.md diff=conv\n")
			must(ctx, dir, "add", ".gitattributes")
			write(ctx, dir, "README.md", "changed\n")
			write(ctx, dir, "AGENTS.md", "changed\n")

			patch, err := gitdiff.Differ{}.Patch(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(patch).To(specs.Contain("+changed\n"))
			_, err = os.Stat(marker)
			ctx.Expect(os.IsNotExist(err)).To(specs.BeTrue())
		})

		s.It("refuses a base that is not a full commit hash", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := gitdiff.Differ{}.Patch(bg, dir, base)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}
