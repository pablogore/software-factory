//go:build unix

package gitdiff_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/domain"
)

// s2 is a worktree whose base has the submodule lib/sub checked out, with
// the job's branch at that base, so only what the test does to the
// submodule's checkout changes it.
type s2 struct {
	hostile
	sub string
}

func newS2(ctx *specs.Context) s2 {
	h := newHostile(ctx)
	must(ctx, h.dir, "switch", "-q", base)
	must(ctx, h.dir, "init", "-q", "lib/sub")
	write(ctx, h.dir, "lib/sub/a.go", "a\n")
	must(ctx, h.dir, "-C", "lib/sub", "add", ".")
	must(ctx, h.dir, "-C", "lib/sub", "commit", "-q", "-m", "a")
	must(ctx, h.dir, "add", "lib/sub")
	must(ctx, h.dir, "commit", "-q", "-m", "base with a submodule")
	h.fork = must(ctx, h.dir, "rev-parse", "HEAD")
	must(ctx, h.dir, "switch", "-q", "factory/job-1")
	must(ctx, h.dir, "reset", "-q", "--hard", h.fork)
	return s2{hostile: h, sub: filepath.Join(h.dir, "lib", "sub")}
}

// edit changes the submodule's a.go without committing it, with a later
// mtime, so git status in the submodule refreshes its index and writes it.
func (w s2) edit(ctx *specs.Context) {
	write(ctx, w.sub, "a.go", "b\n")
	later := time.Now().Add(time.Hour)
	ctx.Expect(os.Chtimes(filepath.Join(w.sub, "a.go"), later, later)).To(specs.BeNil())
}

// list has the Differ list the job's changes, as the gate does.
func (w s2) list(ctx *specs.Context) []domain.FileChange {
	changes, err := w.differ.Changes(context.Background(), job, w.dir, w.fork)
	ctx.Expect(err).To(specs.BeNil())
	return changes
}

// dirtySub is the one change an edited submodule makes.
var dirtySub = []domain.FileChange{{Path: "lib/sub", Kind: domain.ChangeModified, Repo: domain.RepoSubmodule}}

func TestDemoS2(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ on a worktree with a submodule the builder set up", func(s *specs.Spec) {
		bg := context.Background()

		specs.Table(s, submoduleRows(), func(r submoduleRow) string { return r.name }, func(ctx *specs.Context, r submoduleRow) {
			w := newS2(ctx)
			r.plant(ctx, w)
			w.edit(ctx)

			changes := w.list(ctx)

			ctx.Expect(changes).ToEqual(dirtySub)
			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("counts the lines without running a hook the submodule's config defines", func(ctx *specs.Context) {
			w := newS2(ctx)
			submoduleRows()[0].plant(ctx, w)
			w.edit(ctx)

			_, err := w.differ.Lines(bg, job, w.dir, w.fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("runs none of the diff commands, hooks or filters a submodule asks for, set up all at once", func(ctx *specs.Context) {
			w := newS2(ctx)
			for _, r := range submoduleRows() {
				r.plant(ctx, w)
			}
			write(ctx, w.sub, ".git/info/attributes", "*.go filter=mc diff=mt\n")
			must(ctx, w.sub, "config", "diff.external", "touch "+w.marker+"; true")
			must(ctx, w.sub, "config", "diff.mt.textconv", "touch "+w.marker+"; cat")
			// The worktree's own config asks git to diff inside submodules.
			must(ctx, w.dir, "config", "diff.submodule", "diff")
			w.edit(ctx)

			changes := w.list(ctx)
			_, err := w.differ.Lines(bg, job, w.dir, w.fork)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(changes).ToEqual(dirtySub)
			ctx.Expect(w.marked()).To(specs.BeFalse())
		})

		s.It("lists a submodule whose checkout only gained an untracked file as one change", func(ctx *specs.Context) {
			w := newS2(ctx)
			write(ctx, w.sub, "new_test.go", "package x\n")

			ctx.Expect(w.list(ctx)).ToEqual(dirtySub)
		})

		s.It("lists nothing for a submodule whose files were touched but not changed", func(ctx *specs.Context) {
			w := newS2(ctx)
			later := time.Now().Add(time.Hour)
			ctx.Expect(os.Chtimes(filepath.Join(w.sub, "a.go"), later, later)).To(specs.BeNil())

			ctx.Expect(w.list(ctx)).To(specs.BeEmpty())
		})

		s.It("lists a submodule whose folder a symlink on the way leads out of the worktree as one change", func(ctx *specs.Context) {
			w := newS2(ctx)
			outside := filepath.Join(ctx.Testing().TempDir(), "lib")
			ctx.Expect(os.Rename(filepath.Join(w.dir, "lib"), outside)).To(specs.BeNil())
			ctx.Expect(os.Symlink(outside, filepath.Join(w.dir, "lib"))).To(specs.BeNil())

			changes := w.list(ctx)

			ctx.Expect(changes).To(specs.Contain(domain.FileChange{Path: "lib/sub", Kind: domain.ChangeDeleted, Repo: domain.RepoSubmodule}))
		})

		s.It("fails when the submodule's core.worktree moves its toplevel out of its folder", func(ctx *specs.Context) {
			w := newS2(ctx)
			w.edit(ctx)
			must(ctx, w.sub, "config", "core.worktree", ctx.Testing().TempDir())

			_, err := w.differ.Changes(bg, job, w.dir, w.fork)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("toplevel"))
		})
	})

	specs.Describe(t, "gitdiff.Differ on a worktree whose toplevel the builder moved", func(s *specs.Spec) {
		bg := context.Background()

		specs.Table(s, movedTopRows(), func(r movedTopRow) string { return r.name }, func(ctx *specs.Context, r movedTopRow) {
			dir, fork := newRepo(ctx)
			d := differ(ctx)
			// What git would read as the worktree's, if it read anything there.
			outside := r.outside(ctx, dir)
			write(ctx, outside, "README.md", "outside\n")
			write(ctx, outside, "secret.txt", "outside\n")
			r.move(ctx, dir, outside)
			// The premise: git takes outside as the toplevel.
			top, err := filepath.EvalSymlinks(must(ctx, dir, "rev-parse", "--show-toplevel"))
			ctx.Expect(err).To(specs.BeNil())
			want, err := filepath.EvalSymlinks(outside)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(top).ToEqual(want)

			changes, errChanges := d.Changes(bg, job, dir, fork)
			lines, errLines := d.Lines(bg, job, dir, fork)
			content, errRead := d.InWorktree(bg, job, dir, "README.md")

			for _, err := range []error{errChanges, errLines, errRead} {
				ctx.Expect(err).To(specs.Not(specs.BeNil()))
				ctx.Expect(err.Error()).To(specs.Contain("toplevel"))
			}
			ctx.Expect(changes).To(specs.BeEmpty())
			ctx.Expect(lines).ToEqual(0)
			ctx.Expect(content).ToEqual("")
		})
	})

	specs.Describe(t, "gitdiff.Differ copying the real index", func(s *specs.Spec) {
		s.It("opens it without taking a terminal there as factoryd's controlling one", func(ctx *specs.Context) {
			ctx.Expect(gitdiff.IndexFlags & syscall.O_NOCTTY).ToEqual(syscall.O_NOCTTY)
		})
	})
}

// submoduleRow sets up the submodule to run the marker when git runs in it,
// as git status does in a submodule whose index it refreshes.
type submoduleRow struct {
	name  string
	plant func(ctx *specs.Context, w s2)
}

func submoduleRows() []submoduleRow {
	return []submoduleRow{
		{"runs no hook the submodule's config defines by name", func(ctx *specs.Context, w s2) {
			must(ctx, w.sub, "config", "hook.m.command", "touch "+w.marker)
			must(ctx, w.sub, "config", "hook.m.event", "post-index-change")
		}},
		{"runs no clean filter the submodule's config names", func(ctx *specs.Context, w s2) {
			write(ctx, w.sub, ".git/info/attributes", "*.go filter=mc\n")
			must(ctx, w.sub, "config", "filter.mc.clean", "touch "+w.marker+"; cat")
		}},
		{"runs no process filter the submodule's config names", func(ctx *specs.Context, w s2) {
			write(ctx, w.sub, ".git/info/attributes", "*.go filter=mp\n")
			must(ctx, w.sub, "config", "filter.mp.process", "touch "+w.marker)
		}},
	}
}

// movedTopRow moves the toplevel of the repo at dir to outside, a folder
// outside picks.
type movedTopRow struct {
	name    string
	outside func(ctx *specs.Context, dir string) string
	move    func(ctx *specs.Context, dir, outside string)
}

func movedTopRows() []movedTopRow {
	elsewhere := func(ctx *specs.Context, _ string) string { return ctx.Testing().TempDir() }
	return []movedTopRow{
		{"fails when core.worktree in the config moves the toplevel to another folder", elsewhere, func(ctx *specs.Context, dir, outside string) {
			must(ctx, dir, "config", "core.worktree", outside)
		}},
		{"fails when core.worktree moves the toplevel to the folder above the worktree", func(_ *specs.Context, dir string) string {
			return filepath.Dir(dir)
		}, func(ctx *specs.Context, dir, outside string) {
			must(ctx, dir, "config", "core.worktree", outside)
		}},
		{"fails when core.worktree in config.worktree moves the toplevel", elsewhere, func(ctx *specs.Context, dir, outside string) {
			must(ctx, dir, "config", "extensions.worktreeConfig", "true")
			must(ctx, dir, "config", "--worktree", "core.worktree", outside)
		}},
		{"fails when a rewritten .git points at a git folder whose core.worktree moves the toplevel", elsewhere, func(ctx *specs.Context, dir, outside string) {
			gitDir := filepath.Join(ctx.Testing().TempDir(), "real.git")
			ctx.Expect(os.Rename(filepath.Join(dir, ".git"), gitDir)).To(specs.BeNil())
			write(ctx, dir, ".git", "gitdir: "+gitDir+"\n")
			must(ctx, dir, "config", "core.worktree", outside)
		}},
		// As .worktrees/<id> sits in the main checkout: with no .git of its
		// own, git finds the repo of the folder above, which has the base.
		{"fails when the worktree's .git is deleted and git finds the repo the worktree sits in", func(_ *specs.Context, dir string) string {
			return filepath.Dir(dir)
		}, func(ctx *specs.Context, dir, outside string) {
			ctx.Expect(os.Rename(filepath.Join(dir, ".git"), filepath.Join(outside, ".git"))).To(specs.BeNil())
		}},
	}
}
