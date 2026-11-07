package gitdiff_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/domain"
)

const base = "arch/base"

// must runs git in dir with no user or system config, so the machine's
// settings never change what a test sees.
func must(ctx *specs.Context, dir string, args ...string) {
	ctx.Testing().Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		ctx.Testing().Log(string(out))
	}
	ctx.Expect(err).To(specs.BeNil())
}

func write(ctx *specs.Context, dir, name, content string) {
	ctx.Testing().Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	ctx.Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(path, []byte(content), 0o600)).To(specs.BeNil())
}

// newRepo makes a repo with Makefile, AGENTS.md and README.md on base, and a
// job branch off it, checked out.
func newRepo(ctx *specs.Context) string {
	dir := ctx.Testing().TempDir()
	must(ctx, dir, "init", "-q", "-b", base)
	for _, name := range []string{"Makefile", "AGENTS.md", "README.md"} {
		write(ctx, dir, name, name+"\n")
	}
	must(ctx, dir, "add", ".")
	must(ctx, dir, "commit", "-q", "-m", "base")
	must(ctx, dir, "switch", "-q", "-c", "factory/job-1")
	return dir
}

func TestDiffer(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ", func(s *specs.Spec) {
		bg := context.Background()

		s.It("lists nothing for a branch that changed nothing", func(ctx *specs.Context) {
			changes, err := gitdiff.Differ{}.Changes(bg, newRepo(ctx), base)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).To(specs.BeEmpty())
		})

		s.It("lists what the branch's commits added, modified and deleted", func(ctx *specs.Context) {
			dir := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")
			write(ctx, dir, "docs/new file.md", "new\n")
			must(ctx, dir, "rm", "-q", "AGENTS.md")
			must(ctx, dir, "add", ".")
			must(ctx, dir, "commit", "-q", "-m", "job")

			changes, err := gitdiff.Differ{}.Changes(bg, dir, base)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "AGENTS.md", Kind: domain.ChangeDeleted},
				{Path: "Makefile", Kind: domain.ChangeModified},
				{Path: "docs/new file.md", Kind: domain.ChangeAdded},
			})
		})

		s.It("lists tracked changes not yet committed", func(ctx *specs.Context) {
			dir := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")

			changes, err := gitdiff.Differ{}.Changes(bg, dir, base)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("lists a rename as a deletion and an addition", func(ctx *specs.Context) {
			dir := newRepo(ctx)
			must(ctx, dir, "mv", "Makefile", "Makefile.old")
			must(ctx, dir, "commit", "-q", "-m", "job")

			changes, err := gitdiff.Differ{}.Changes(bg, dir, base)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "Makefile", Kind: domain.ChangeDeleted},
				{Path: "Makefile.old", Kind: domain.ChangeAdded},
			})
		})

		s.It("leaves out what the base gained after the branch left it", func(ctx *specs.Context) {
			dir := newRepo(ctx)
			must(ctx, dir, "switch", "-q", base)
			write(ctx, dir, "Makefile", "base moved on\n")
			must(ctx, dir, "commit", "-q", "-am", "base moves")
			must(ctx, dir, "switch", "-q", "factory/job-1")

			changes, err := gitdiff.Differ{}.Changes(bg, dir, base)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).To(specs.BeEmpty())
		})

		s.It("fails for a base the repo does not have", func(ctx *specs.Context) {
			_, err := gitdiff.Differ{}.Changes(bg, newRepo(ctx), "no-such-base")

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(strings.Contains(err.Error(), "merge-base")).To(specs.BeTrue())
		})

		s.It("refuses an empty base", func(ctx *specs.Context) {
			_, err := gitdiff.Differ{}.Changes(bg, newRepo(ctx), "")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
