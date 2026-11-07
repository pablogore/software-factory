package gitworktree_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/gitworktree"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

const base = "arch/base"

// run runs git in dir with no user or system config, so the machine's
// settings never change what a test sees.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func must(ctx *specs.Context, dir string, args ...string) string {
	ctx.Testing().Helper()
	out, err := run(dir, args...)
	ctx.Expect(err).To(specs.BeNil())
	return out
}

// newRepo makes a repo whose base branch is behind main, so a job branch
// made off HEAD instead of the base shows.
func newRepo(ctx *specs.Context) string {
	dir := ctx.Testing().TempDir()
	must(ctx, dir, "init", "-q", "-b", "main")
	ctx.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o600)).To(specs.BeNil())
	must(ctx, dir, "add", "README.md")
	must(ctx, dir, "commit", "-q", "-m", "base")
	must(ctx, dir, "branch", base)
	must(ctx, dir, "commit", "-q", "--allow-empty", "-m", "ahead of base")
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasBranch(repo, branch string) bool {
	_, err := run(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

type dirtyRow struct {
	name  string
	dirty func(wt string) error
}

func TestWorkspace(t *testing.T) {
	specs.Describe(t, "gitworktree.Workspace", func(s *specs.Spec) {
		bg := context.Background()
		const job domain.JobID = "job-1"

		s.It("creates a new job's worktree in .worktrees/<job-id> on a new branch factory/<job-id> off the base", func(ctx *specs.Context) {
			repo := newRepo(ctx)

			wt, err := gitworktree.New(repo, base).Prepare(bg, job)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(wt).ToEqual(ports.Worktree{Job: job, Path: filepath.Join(repo, ".worktrees", "job-1"), Branch: "factory/job-1"})
			ctx.Expect(must(ctx, wt.Path, "rev-parse", "--abbrev-ref", "HEAD")).ToEqual("factory/job-1")
			ctx.Expect(must(ctx, wt.Path, "rev-parse", "HEAD")).ToEqual(must(ctx, repo, "rev-parse", base))
		})

		s.It("reuses an existing worktree without error, leaving what is in it", func(ctx *specs.Context) {
			repo := newRepo(ctx)
			ws := gitworktree.New(repo, base)
			first, err := ws.Prepare(bg, job)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(first.Path, "README.md"), []byte("work in progress\n"), 0o600)).To(specs.BeNil())
			must(ctx, first.Path, "commit", "-q", "--allow-empty", "-m", "job work")
			head := must(ctx, first.Path, "rev-parse", "HEAD")

			again, err := ws.Prepare(bg, job)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(again).ToEqual(first)
			ctx.Expect(must(ctx, again.Path, "rev-parse", "HEAD")).ToEqual(head)
			got, err := os.ReadFile(filepath.Join(again.Path, "README.md"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(string(got)).ToEqual("work in progress\n")
		})

		s.It("cleans a job without pending changes: its worktree and local branch go, the base and other jobs stay", func(ctx *specs.Context) {
			repo := newRepo(ctx)
			ws := gitworktree.New(repo, base)
			baseSHA := must(ctx, repo, "rev-parse", base)
			wt, err := ws.Prepare(bg, job)
			ctx.Expect(err).To(specs.BeNil())
			must(ctx, wt.Path, "commit", "-q", "--allow-empty", "-m", "job work")
			other, err := ws.Prepare(bg, "job-2")
			ctx.Expect(err).To(specs.BeNil())

			err = ws.Clean(bg, job)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(exists(wt.Path)).To(specs.BeFalse())
			ctx.Expect(hasBranch(repo, wt.Branch)).To(specs.BeFalse())
			ctx.Expect(strings.Contains(must(ctx, repo, "worktree", "list"), filepath.Join(".worktrees", "job-1"))).To(specs.BeFalse())
			ctx.Expect(must(ctx, repo, "rev-parse", base)).ToEqual(baseSHA)
			ctx.Expect(exists(other.Path)).To(specs.BeTrue())
			ctx.Expect(hasBranch(repo, other.Branch)).To(specs.BeTrue())
		})

		rows := []dirtyRow{
			{"a modified tracked file", func(wt string) error {
				return os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed\n"), 0o600)
			}},
			{"an untracked file", func(wt string) error {
				return os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o600)
			}},
		}
		specs.Table(s, rows, func(r dirtyRow) string {
			return "refuses to clean a worktree with " + r.name + ", deleting nothing"
		}, func(ctx *specs.Context, r dirtyRow) {
			repo := newRepo(ctx)
			ws := gitworktree.New(repo, base)
			wt, err := ws.Prepare(bg, job)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(r.dirty(wt.Path)).To(specs.BeNil())
			before := must(ctx, wt.Path, "status", "--porcelain")

			err = ws.Clean(bg, job)

			ctx.Expect(err).To(specs.MatchError(ports.ErrUncommittedChanges))
			ctx.Expect(err.Error()).To(specs.Contain(wt.Path))
			ctx.Expect(exists(wt.Path)).To(specs.BeTrue())
			ctx.Expect(hasBranch(repo, wt.Branch)).To(specs.BeTrue())
			ctx.Expect(must(ctx, wt.Path, "status", "--porcelain")).ToEqual(before)
		})

		s.It("refuses a job id that would leave .worktrees", func(ctx *specs.Context) {
			repo := newRepo(ctx)

			_, err := gitworktree.New(repo, base).Prepare(bg, "../escape")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(exists(filepath.Join(repo, "escape"))).To(specs.BeFalse())
		})
	})
}
