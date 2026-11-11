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
	"github.com/pablogore/software-factory/internal/platform/childenv"
)

const base = "arch/base"

// job is the job the tests run git for.
const job domain.JobID = "job-1"

// differ is a Differ with the jobs' HOMEs in the test's own directory.
func differ(ctx *specs.Context) gitdiff.Differ {
	return gitdiff.Differ{Homes: childenv.Homes{Root: ctx.Testing().TempDir()}}
}

// must runs git in dir with no user or system config, so the machine's
// settings never change what a test sees, and returns its trimmed output.
func must(ctx *specs.Context, dir string, args ...string) string {
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
	return strings.TrimSpace(string(out))
}

func write(ctx *specs.Context, dir, name, content string) {
	ctx.Testing().Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	ctx.Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(path, []byte(content), 0o600)).To(specs.BeNil())
}

// newRepo makes a repo with Makefile, AGENTS.md and README.md on base, and a
// job branch off it, checked out. It returns the repo and the commit the job
// branch was made from, as factoryd records it.
func newRepo(ctx *specs.Context) (string, string) {
	return newRepoWith(ctx, nil)
}

// newRepoWith is newRepo with files, by path and content, also on base.
func newRepoWith(ctx *specs.Context, files map[string]string) (string, string) {
	dir := ctx.Testing().TempDir()
	must(ctx, dir, "init", "-q", "-b", base)
	for _, name := range []string{"Makefile", "AGENTS.md", "README.md"} {
		write(ctx, dir, name, name+"\n")
	}
	for name, content := range files {
		write(ctx, dir, name, content)
	}
	must(ctx, dir, "add", ".")
	must(ctx, dir, "commit", "-q", "-m", "base")
	must(ctx, dir, "switch", "-q", "-c", "factory/job-1")
	return dir, must(ctx, dir, "rev-parse", base)
}

func TestDiffer(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ", func(s *specs.Spec) {
		bg := context.Background()

		s.It("lists nothing for a branch that changed nothing", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).To(specs.BeEmpty())
		})

		s.It("lists what the branch's commits added, modified and deleted", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")
			write(ctx, dir, "docs/new file.md", "new\n")
			must(ctx, dir, "rm", "-q", "AGENTS.md")
			must(ctx, dir, "add", ".")
			must(ctx, dir, "commit", "-q", "-m", "job")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "AGENTS.md", Kind: domain.ChangeDeleted},
				{Path: "Makefile", Kind: domain.ChangeModified},
				{Path: "docs/new file.md", Kind: domain.ChangeAdded},
			})
		})

		s.It("lists tracked changes not yet committed", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("lists an untracked file as added, after the diff, and leaves out those the base's .gitignore files ignore", func(ctx *specs.Context) {
			dir, fork := newRepoWith(ctx, map[string]string{
				".gitignore":     "*.log\nbin/\n",
				"sub/.gitignore": "/only\n!keep.log\n",
			})
			write(ctx, dir, "README.md", "changed\n")
			write(ctx, dir, "notes/new.txt", "new\n")
			write(ctx, dir, "build.log", "ignored\n")
			write(ctx, dir, "bin/deep/tool", "ignored\n")
			write(ctx, dir, "sub/only", "ignored\n")
			write(ctx, dir, "sub/keep.log", "kept\n")
			write(ctx, dir, "only", "kept\n")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "README.md", Kind: domain.ChangeModified},
				{Path: "notes/new.txt", Kind: domain.ChangeAdded},
				{Path: "only", Kind: domain.ChangeAdded},
				{Path: "sub/keep.log", Kind: domain.ChangeAdded},
			})
		})

		s.It("reads untracked paths that look like pathspec magic as plain paths", func(ctx *specs.Context) {
			dir, fork := newRepoWith(ctx, map[string]string{".gitignore": "/bin/\n"})
			write(ctx, dir, ":/bin/x_test.go", "t.Skip()\n")
			write(ctx, dir, ":(top)bin/z", "hidden\n")
			write(ctx, dir, ":(exclude)x", "new\n")
			write(ctx, dir, "bin/y", "ignored\n")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: ":(exclude)x", Kind: domain.ChangeAdded},
				{Path: ":(top)bin/z", Kind: domain.ChangeAdded},
				{Path: ":/bin/x_test.go", Kind: domain.ChangeAdded},
			})
		})

		s.It("counts the lines committed or not, untracked files included, and none of an ignored or binary file", func(ctx *specs.Context) {
			dir, fork := newRepoWith(ctx, map[string]string{".gitignore": "*.log\n"})
			write(ctx, dir, "a.go", "one\ntwo\n")
			must(ctx, dir, "add", "a.go")
			must(ctx, dir, "commit", "-qm", "a")
			write(ctx, dir, "README.md", "changed\n")
			write(ctx, dir, "notes/new.txt", "x\ny\nno newline")
			write(ctx, dir, "build.log", "ignored\nignored\n")
			write(ctx, dir, "blob.bin", "\x00\x01\n")
			must(ctx, dir, "rm", "-q", "AGENTS.md")

			lines, err := differ(ctx).Lines(bg, job, dir, fork)

			// a.go 2, README.md 1 added and 1 deleted, AGENTS.md 1 deleted,
			// notes/new.txt 3.
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(lines).ToEqual(8)
		})

		s.It("lists an untracked file .git/info/exclude or core.excludesFile hides", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, ".git/info/exclude", "hidden_test.go\n")
			excludes := filepath.Join(ctx.Testing().TempDir(), "excludes")
			write(ctx, filepath.Dir(excludes), "excludes", "also_test.go\n")
			must(ctx, dir, "config", "core.excludesFile", excludes)
			write(ctx, dir, "hidden_test.go", "t.Skip()\n")
			write(ctx, dir, "also_test.go", "t.Skip()\n")
			// The premise: git's own exclude rules hide both.
			ctx.Expect(must(ctx, dir, "ls-files", "--others", "--exclude-standard")).ToEqual("")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "also_test.go", Kind: domain.ChangeAdded},
				{Path: "hidden_test.go", Kind: domain.ChangeAdded},
			})
		})

		s.It("lists an untracked file an ignore rule the branch added hides", func(ctx *specs.Context) {
			dir, fork := newRepoWith(ctx, map[string]string{".gitignore": "*.log\n"})
			write(ctx, dir, ".gitignore", "*.log\nhidden_test.go\n")
			write(ctx, dir, "sub/.gitignore", "*\n")
			must(ctx, dir, "add", ".gitignore")
			must(ctx, dir, "commit", "-q", "-m", "job")
			write(ctx, dir, "hidden_test.go", "t.Skip()\n")
			write(ctx, dir, "sub/inner_test.go", "t.Skip()\n")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: ".gitignore", Kind: domain.ChangeModified},
				{Path: "hidden_test.go", Kind: domain.ChangeAdded},
				{Path: "sub/.gitignore", Kind: domain.ChangeAdded},
				{Path: "sub/inner_test.go", Kind: domain.ChangeAdded},
			})
		})

		s.It("lists a change a core.fsmonitor that reports nothing hides", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			hook := filepath.Join(ctx.Testing().TempDir(), "fsmonitor")
			ctx.Expect(os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'token\\0'\n"), 0o700)).To(specs.BeNil())
			must(ctx, dir, "config", "core.fsmonitor", hook)
			must(ctx, dir, "status", "--short")
			write(ctx, dir, "Makefile", "hidden by fsmonitor\n")
			// The premise: git with the fsmonitor trusts the index and sees nothing.
			ctx.Expect(must(ctx, dir, "diff", "--name-only", fork, "--")).ToEqual("")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("lists untracked files by their path from the repo root, and fails when dir is a subfolder", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "top.txt", "new\n")
			write(ctx, dir, "sub/inner.txt", "new\n")

			// Only dir itself can be the toplevel: a folder above it could be
			// another repo git found because the worktree's .git is gone.
			_, err := differ(ctx).Changes(bg, job, filepath.Join(dir, "sub"), fork)
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "sub/inner.txt", Kind: domain.ChangeAdded},
				{Path: "top.txt", Kind: domain.ChangeAdded},
			})
		})

		s.It("lists an untracked folder with its own .git as one nested repo, even where the base's .gitignore ignores it", func(ctx *specs.Context) {
			dir, fork := newRepoWith(ctx, map[string]string{".gitignore": "vendorx/\n"})
			must(ctx, dir, "init", "-q", "vendorx")
			write(ctx, dir, "vendorx/a_test.go", "package x\n")
			must(ctx, dir, "init", "-q", "deep/inner")
			write(ctx, dir, "deep/inner/b_test.go", "package x\n")
			write(ctx, dir, "deep/new.txt", "new\n")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "deep/inner/", Kind: domain.ChangeAdded, Repo: domain.RepoNested},
				{Path: "deep/new.txt", Kind: domain.ChangeAdded},
				{Path: "vendorx/", Kind: domain.ChangeAdded, Repo: domain.RepoNested},
			})
		})

		s.It("lists a submodule added, with no .gitmodules, or moved to another commit as one, whatever the config ignores", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)
			must(ctx, dir, "switch", "-q", base)
			must(ctx, dir, "init", "-q", "lib/sub")
			write(ctx, dir, "lib/sub/a.go", "a\n")
			must(ctx, dir, "-C", "lib/sub", "add", ".")
			must(ctx, dir, "-C", "lib/sub", "commit", "-q", "-m", "a")
			must(ctx, dir, "add", "lib/sub")
			must(ctx, dir, "commit", "-q", "-m", "base with a submodule")
			fork := must(ctx, dir, "rev-parse", "HEAD")
			must(ctx, dir, "switch", "-q", "factory/job-1")
			must(ctx, dir, "reset", "-q", "--hard", fork)
			write(ctx, dir, "lib/sub/a.go", "moved\n")
			must(ctx, dir, "-C", "lib/sub", "commit", "-q", "-am", "moved")
			must(ctx, dir, "init", "-q", "vendorx")
			write(ctx, dir, "vendorx/a_test.go", "package x\n")
			must(ctx, dir, "-C", "vendorx", "add", ".")
			must(ctx, dir, "-C", "vendorx", "commit", "-q", "-m", "x")
			must(ctx, dir, "add", "vendorx")
			must(ctx, dir, "config", "diff.ignoreSubmodules", "all")
			must(ctx, dir, "config", "submodule.lib/sub.ignore", "all")
			// The premise: git with that config sees neither.
			ctx.Expect(must(ctx, dir, "diff", "--name-only", fork, "--")).ToEqual("")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "lib/sub", Kind: domain.ChangeModified, Repo: domain.RepoSubmodule},
				{Path: "vendorx", Kind: domain.ChangeAdded, Repo: domain.RepoSubmodule},
			})
		})

		s.It("lists a rename as a deletion and an addition", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			must(ctx, dir, "mv", "Makefile", "Makefile.old")
			must(ctx, dir, "commit", "-q", "-m", "job")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "Makefile", Kind: domain.ChangeDeleted},
				{Path: "Makefile.old", Kind: domain.ChangeAdded},
			})
		})

		s.It("leaves out what the base gained after the branch left it", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			must(ctx, dir, "switch", "-q", base)
			write(ctx, dir, "Makefile", "base moved on\n")
			must(ctx, dir, "commit", "-q", "-am", "base moves")
			must(ctx, dir, "switch", "-q", "factory/job-1")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).To(specs.BeEmpty())
		})

		s.It("still lists what the branch committed after the base ref is moved onto it", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")
			must(ctx, dir, "commit", "-q", "-am", "job")
			must(ctx, dir, "branch", "-f", base, "HEAD")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("still lists what the branch committed after the base commit is replaced by the branch's head", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")
			must(ctx, dir, "commit", "-q", "-am", "job")
			must(ctx, dir, "replace", fork, "HEAD")

			changes, err := differ(ctx).Changes(bg, job, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("flags nothing in an index that hides no changes", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			flagged, err := differ(ctx).Flagged(bg, job, dir)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(flagged).To(specs.BeEmpty())
		})

		s.It("flags each file the index marks skip-worktree or assume-unchanged, by its path from the root", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "sub/inner.txt", "inner\n")
			must(ctx, dir, "add", ".")
			must(ctx, dir, "commit", "-q", "-m", "sub")
			must(ctx, dir, "update-index", "--skip-worktree", "README.md", "sub/inner.txt")
			must(ctx, dir, "update-index", "--assume-unchanged", "Makefile", "README.md")
			write(ctx, dir, "Makefile", "hidden\n")

			flagged, err := differ(ctx).Flagged(bg, job, filepath.Join(dir, "sub"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(flagged).ToEqual([]domain.FlaggedFile{
				{Path: "Makefile", Flag: domain.FlagAssumeUnchanged},
				{Path: "README.md", Flag: domain.FlagSkipWorktree},
				{Path: "README.md", Flag: domain.FlagAssumeUnchanged},
				{Path: "sub/inner.txt", Flag: domain.FlagSkipWorktree},
			})
			// The flags hide the Makefile's change from git diff on this index,
			// but not from Changes, whose own index has no flags.
			ctx.Expect(must(ctx, dir, "diff", "--name-only", fork, "--")).To(specs.Not(specs.Contain("Makefile")))
			changes, err := differ(ctx).Changes(bg, job, dir, fork)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "Makefile", Kind: domain.ChangeModified},
				{Path: "sub/inner.txt", Kind: domain.ChangeAdded},
			})
		})

		s.It("reads a file as the base commit has it, whatever the worktree did to it", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "sub/a_test.go", "committed\n")
			must(ctx, dir, "add", ".")
			must(ctx, dir, "commit", "-q", "-m", "job")
			write(ctx, dir, "README.md", "changed\n")

			readme, err := differ(ctx).AtBase(bg, job, filepath.Join(dir, "sub"), fork, "README.md")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(readme).ToEqual("README.md\n")
			_, err = differ(ctx).AtBase(bg, job, dir, fork, "sub/a_test.go")
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})

		s.It("reads a file the base commit may not have, and says when it has none", func(ctx *specs.Context) {
			dir, fork := newRepoWith(ctx, map[string]string{".factory/gates.yaml": "count_command: go test ./...\n"})
			write(ctx, dir, ".factory/gates.yaml", "count_command: true\n")
			write(ctx, dir, "new.txt", "new\n")
			must(ctx, dir, "add", ".")
			must(ctx, dir, "commit", "-q", "-m", "job")

			gates, ok, err := differ(ctx).AtBaseIfAny(bg, job, filepath.Join(dir, ".factory"), fork, ".factory/gates.yaml")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(gates).ToEqual("count_command: go test ./...\n")
			_, ok, err = differ(ctx).AtBaseIfAny(bg, job, dir, fork, "new.txt")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(ok).To(specs.BeFalse())
			_, _, err = differ(ctx).AtBaseIfAny(bg, job, dir, fork, ".factory")
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			_, _, err = differ(ctx).AtBaseIfAny(bg, job, dir, "HEAD", "README.md")
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("refuses a ref as the base to read a file at", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := differ(ctx).AtBase(bg, job, dir, "HEAD", "README.md")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("reads a file as the worktree has it, uncommitted or untracked, by its path from the root, which dir must be", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)
			write(ctx, dir, "README.md", "changed\n")
			write(ctx, dir, "sub/new_test.go", "t.Skip()\n")

			_, err := differ(ctx).InWorktree(bg, job, filepath.Join(dir, "sub"), "README.md")
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			readme, err := differ(ctx).InWorktree(bg, job, dir, "README.md")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(readme).ToEqual("changed\n")
			untracked, err := differ(ctx).InWorktree(bg, job, dir, "sub/new_test.go")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(untracked).ToEqual("t.Skip()\n")
		})

		s.It("reads a symlink in the worktree as its target, as git keeps it", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)
			outside := filepath.Join(ctx.Testing().TempDir(), "outside_test.go")
			write(ctx, filepath.Dir(outside), "outside_test.go", "t.Skip()\n")
			ctx.Expect(os.Symlink(outside, filepath.Join(dir, "link_test.go"))).To(specs.BeNil())

			got, err := differ(ctx).InWorktree(bg, job, dir, "link_test.go")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(outside)
		})

		s.It("fails for a base commit the repo does not have", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := differ(ctx).Changes(bg, job, dir, strings.Repeat("0", 40))

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("git diff"))
		})

		s.It("fails for a base object that is not a commit", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			tree := must(ctx, dir, "rev-parse", fork+"^{tree}")

			_, err := differ(ctx).Changes(bg, job, dir, tree)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("git diff"))
		})

		s.It("refuses a ref, or a short hash, as base", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)

			for _, ref := range []string{base, "HEAD", fork[:12], strings.ToUpper(fork)} {
				_, err := differ(ctx).Changes(bg, job, dir, ref)

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			}
		})

		s.It("refuses an empty base", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := differ(ctx).Changes(bg, job, dir, "")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
