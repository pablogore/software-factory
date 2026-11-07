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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "README.md", Kind: domain.ChangeModified},
				{Path: "notes/new.txt", Kind: domain.ChangeAdded},
				{Path: "only", Kind: domain.ChangeAdded},
				{Path: "sub/keep.log", Kind: domain.ChangeAdded},
			})
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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("lists untracked files by their path from the repo root when dir is a subfolder", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "top.txt", "new\n")
			write(ctx, dir, "sub/inner.txt", "new\n")

			changes, err := gitdiff.Differ{}.Changes(bg, filepath.Join(dir, "sub"), fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{
				{Path: "sub/inner.txt", Kind: domain.ChangeAdded},
				{Path: "top.txt", Kind: domain.ChangeAdded},
			})
		})

		s.It("lists a rename as a deletion and an addition", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			must(ctx, dir, "mv", "Makefile", "Makefile.old")
			must(ctx, dir, "commit", "-q", "-m", "job")

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

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

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).To(specs.BeEmpty())
		})

		s.It("still lists what the branch committed after the base ref is moved onto it", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")
			must(ctx, dir, "commit", "-q", "-am", "job")
			must(ctx, dir, "branch", "-f", base, "HEAD")

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("still lists what the branch committed after the base commit is replaced by the branch's head", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "Makefile", "changed\n")
			must(ctx, dir, "commit", "-q", "-am", "job")
			must(ctx, dir, "replace", fork, "HEAD")

			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}})
		})

		s.It("flags nothing in an index that hides no changes", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			flagged, err := gitdiff.Differ{}.Flagged(bg, dir)

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

			flagged, err := gitdiff.Differ{}.Flagged(bg, filepath.Join(dir, "sub"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(flagged).ToEqual([]domain.FlaggedFile{
				{Path: "Makefile", Flag: domain.FlagAssumeUnchanged},
				{Path: "README.md", Flag: domain.FlagSkipWorktree},
				{Path: "README.md", Flag: domain.FlagAssumeUnchanged},
				{Path: "sub/inner.txt", Flag: domain.FlagSkipWorktree},
			})
			// The flags are why: the diff no longer sees the Makefile's change.
			changes, err := gitdiff.Differ{}.Changes(bg, dir, fork)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual([]domain.FileChange{{Path: "sub/inner.txt", Kind: domain.ChangeAdded}})
		})

		s.It("reads a file as the base commit has it, whatever the worktree did to it", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			write(ctx, dir, "sub/a_test.go", "committed\n")
			must(ctx, dir, "add", ".")
			must(ctx, dir, "commit", "-q", "-m", "job")
			write(ctx, dir, "README.md", "changed\n")

			readme, err := gitdiff.Differ{}.AtBase(bg, filepath.Join(dir, "sub"), fork, "README.md")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(readme).ToEqual("README.md\n")
			_, err = gitdiff.Differ{}.AtBase(bg, dir, fork, "sub/a_test.go")
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})

		s.It("refuses a ref as the base to read a file at", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := gitdiff.Differ{}.AtBase(bg, dir, "HEAD", "README.md")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("reads a file as the worktree has it, uncommitted or untracked, by its path from the root", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)
			write(ctx, dir, "README.md", "changed\n")
			write(ctx, dir, "sub/new_test.go", "t.Skip()\n")

			readme, err := gitdiff.Differ{}.InWorktree(bg, filepath.Join(dir, "sub"), "README.md")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(readme).ToEqual("changed\n")
			untracked, err := gitdiff.Differ{}.InWorktree(bg, dir, "sub/new_test.go")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(untracked).ToEqual("t.Skip()\n")
		})

		s.It("reads a symlink in the worktree as its target, as git keeps it", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)
			outside := filepath.Join(ctx.Testing().TempDir(), "outside_test.go")
			write(ctx, filepath.Dir(outside), "outside_test.go", "t.Skip()\n")
			ctx.Expect(os.Symlink(outside, filepath.Join(dir, "link_test.go"))).To(specs.BeNil())

			got, err := gitdiff.Differ{}.InWorktree(bg, dir, "link_test.go")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(outside)
		})

		s.It("fails for a base commit the repo does not have", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := gitdiff.Differ{}.Changes(bg, dir, strings.Repeat("0", 40))

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("git diff"))
		})

		s.It("fails for a base object that is not a commit", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)
			tree := must(ctx, dir, "rev-parse", fork+"^{tree}")

			_, err := gitdiff.Differ{}.Changes(bg, dir, tree)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("git diff"))
		})

		s.It("refuses a ref, or a short hash, as base", func(ctx *specs.Context) {
			dir, fork := newRepo(ctx)

			for _, ref := range []string{base, "HEAD", fork[:12], strings.ToUpper(fork)} {
				_, err := gitdiff.Differ{}.Changes(bg, dir, ref)

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			}
		})

		s.It("refuses an empty base", func(ctx *specs.Context) {
			dir, _ := newRepo(ctx)

			_, err := gitdiff.Differ{}.Changes(bg, dir, "")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
