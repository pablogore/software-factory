package gitdiff_test

import (
	"context"
	"os"
	"os/exec"
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

		s.It("never runs a clean or process filter the attributes or the config name", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			tmp := ctx.Testing().TempDir()
			marker := filepath.Join(tmp, "ran")
			write(ctx, dir, ".gitattributes", "README.md filter=x\n")
			must(ctx, dir, "add", ".gitattributes")
			must(ctx, dir, "config", "filter.x.clean", "touch "+marker+"; cat")
			must(ctx, dir, "config", "filter.y.process", "touch "+marker)
			attributes := filepath.Join(tmp, "attributes")
			ctx.Expect(os.WriteFile(attributes, []byte("Makefile filter=x\n"), 0o600)).To(specs.BeNil())
			must(ctx, dir, "config", "core.attributesFile", attributes)
			write(ctx, dir, ".git/info/attributes", "AGENTS.md filter=y\n")
			for _, name := range []string{"Makefile", "AGENTS.md", "README.md"} {
				write(ctx, dir, name, "changed\n")
			}
			// The premise: git diff against the worktree runs them.
			_ = exec.Command("git", "-C", dir, "diff", fork, "--").Run()
			_, err := os.Stat(marker)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(os.Remove(marker)).To(specs.BeNil())

			patch, err := gitdiff.Differ{}.Patch(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			for _, name := range []string{"Makefile", "AGENTS.md", "README.md"} {
				ctx.Expect(patch).To(specs.Contain("diff --git a/" + name + " b/" + name + "\n"))
			}
			ctx.Expect(patch).To(specs.Contain("+changed\n"))
			_, err = os.Stat(marker)
			ctx.Expect(os.IsNotExist(err)).To(specs.BeTrue())
		})

		s.It("shows a deleted file, and a symlink as its target, never reading outside the worktree", func(ctx *specs.Context) {
			dir, fork := newRepoWith(ctx, map[string]string{"conf/key": "base key\n"})
			outside := ctx.Testing().TempDir()
			write(ctx, outside, "secret", "outside secret\n")
			write(ctx, outside, "key", "outside key\n")
			ctx.Expect(os.Remove(filepath.Join(dir, "Makefile"))).To(specs.BeNil())
			ctx.Expect(os.Remove(filepath.Join(dir, "README.md"))).To(specs.BeNil())
			ctx.Expect(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "README.md"))).To(specs.BeNil())
			ctx.Expect(os.RemoveAll(filepath.Join(dir, "conf"))).To(specs.BeNil())
			ctx.Expect(os.Symlink(outside, filepath.Join(dir, "conf"))).To(specs.BeNil())

			patch, err := gitdiff.Differ{}.Patch(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(patch).To(specs.Contain("diff --git a/Makefile b/Makefile\ndeleted file mode 100644\n"))
			ctx.Expect(patch).To(specs.Contain("+" + filepath.Join(outside, "secret") + "\n"))
			ctx.Expect(patch).To(specs.Contain("diff --git a/conf/key b/conf/key\ndeleted file mode 100644\n"))
			ctx.Expect(patch).To(specs.Not(specs.Contain("outside")))
		})

		s.It("refuses a base that is not a full commit hash", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := gitdiff.Differ{}.Patch(bg, dir, base)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}
