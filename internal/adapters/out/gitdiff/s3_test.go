//go:build unix

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
	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// s3 is a repo whose base has data.txt with a filter in .gitattributes,
// with a Differ whose jobs' HOMEs are the test's own, job's HOME in it, and
// a marker path no file is at yet.
type s3 struct {
	dir, fork, home, marker string
	differ                  gitdiff.Differ
}

func newS3(ctx *specs.Context) s3 {
	ctx.Testing().Setenv("GITHUB_TOKEN", token)
	dir, fork := newRepoWith(ctx, map[string]string{"data.txt": "data\n", ".gitattributes": "data.txt filter=evil\n"})
	homes := childenv.Homes{Root: ctx.Testing().TempDir()}
	home, err := homes.For(job)
	ctx.Expect(err).To(specs.BeNil())
	return s3{dir: dir, fork: fork, home: home, marker: filepath.Join(ctx.Testing().TempDir(), "marker"), differ: gitdiff.Differ{Homes: homes}}
}

func (w s3) dump() string {
	return "env > " + w.marker + "; true"
}

// copyEvents are the hooks git runs as it checks a commit out apart.
var copyEvents = []string{"post-checkout", "reference-transaction"}

func (w s3) hooks(ctx *specs.Context, dir string) {
	for _, ev := range copyEvents {
		write(ctx, dir, ev, "#!/bin/sh\n"+w.dump()+"\n")
		ctx.Expect(os.Chmod(filepath.Join(dir, ev), 0o700)).To(specs.BeNil())
	}
}

func (w s3) configHook(ctx *specs.Context, file string) {
	must(ctx, w.dir, "config", "--file", file, "hook.evil.command", w.dump())
	for _, ev := range copyEvents {
		must(ctx, w.dir, "config", "--file", file, "--add", "hook.evil.event", ev)
	}
}

func (w s3) marked() bool {
	_, err := os.Stat(w.marker)
	return err == nil
}

// linkTarget makes dir/link a link to a folder in a new temporary folder,
// and returns that temporary folder: the one dir/link/.. is.
func linkTarget(ctx *specs.Context, dir string) string {
	ctx.Testing().Helper()
	elsewhere := ctx.Testing().TempDir()
	inner := filepath.Join(elsewhere, "inner")
	ctx.Expect(os.Mkdir(inner, 0o700)).To(specs.BeNil())
	ctx.Expect(os.Symlink(inner, filepath.Join(dir, "link"))).To(specs.BeNil())
	return elsewhere
}

type s3Row struct {
	name string
	arm  func(ctx *specs.Context, w s3)
}

func s3Rows() []s3Row {
	return []s3Row{
		{"a hook in .git/hooks", func(ctx *specs.Context, w s3) {
			w.hooks(ctx, filepath.Join(w.dir, ".git", "hooks"))
		}},
		{"a hook in the folder core.hooksPath names", func(ctx *specs.Context, w s3) {
			hooks := filepath.Join(ctx.Testing().TempDir(), "hooks")
			w.hooks(ctx, hooks)
			must(ctx, w.dir, "config", "core.hooksPath", hooks)
		}},
		{"a hook the config defines", func(ctx *specs.Context, w s3) {
			w.configHook(ctx, filepath.Join(w.dir, ".git", "config"))
		}},
		{"a hook a file the config includes from HOME defines", func(ctx *specs.Context, w s3) {
			w.configHook(ctx, filepath.Join(w.home, "evil.inc"))
			must(ctx, w.dir, "config", "include.path", "~/evil.inc")
		}},
		// git doesn't clean an include's path: link/.. is the parent of the
		// link's target, not the folder the link is in.
		{"a hook a file the config includes from HOME through a link and .. defines", func(ctx *specs.Context, w s3) {
			w.configHook(ctx, filepath.Join(linkTarget(ctx, w.home), "evil.inc"))
			must(ctx, w.dir, "config", "include.path", "~/link/../evil.inc")
		}},
		{"a hook a file the config includes by a relative path through a link and .. defines", func(ctx *specs.Context, w s3) {
			w.configHook(ctx, filepath.Join(linkTarget(ctx, filepath.Join(w.dir, ".git")), "evil.inc"))
			must(ctx, w.dir, "config", "include.path", "link/../evil.inc")
		}},
		{"a smudge filter the commit's .gitattributes names", func(ctx *specs.Context, w s3) {
			must(ctx, w.dir, "config", "filter.evil.smudge", w.dump()+"; cat")
		}},
		{"a required process filter the commit's .gitattributes names", func(ctx *specs.Context, w s3) {
			must(ctx, w.dir, "config", "filter.evil.process", w.dump())
			must(ctx, w.dir, "config", "filter.evil.required", "true")
		}},
		{"a filter info/attributes names", func(ctx *specs.Context, w s3) {
			write(ctx, w.dir, ".git/info/attributes", "*.txt filter=other\n")
			must(ctx, w.dir, "config", "filter.other.smudge", w.dump()+"; cat")
		}},
	}
}

func TestDemoS3(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ's copy of the base on a repo the builder set up", func(s *specs.Spec) {
		bg := context.Background()

		specs.Table(s, s3Rows(), func(r s3Row) string {
			return "checks the base out, with its files as git keeps them, and removes it without running " + r.name
		}, func(ctx *specs.Context, r s3Row) {
			w := newS3(ctx)
			r.arm(ctx, w)

			copyDir, err := w.differ.Checkout(bg, job, w.dir, w.fork)
			ctx.Expect(err).To(specs.BeNil())
			data, err := os.ReadFile(filepath.Join(copyDir, "data.txt"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(w.differ.Remove(bg, job, w.dir, copyDir)).To(specs.BeNil())

			ctx.Expect(w.marked()).To(specs.BeFalse())
			ctx.Expect(string(data)).ToEqual("data\n")
		})

		s.It("hands no token to any git it runs to check the base out and remove it, and the job's HOME to each", func(ctx *specs.Context) {
			w := newS3(ctx)
			gitPath, err := exec.LookPath("git")
			ctx.Expect(err).To(specs.BeNil())
			// From here on every git found on PATH logs its environment.
			shim := ctx.Testing().TempDir()
			write(ctx, shim, "git", "#!/bin/sh\n{ echo \"--- git $*\"; env; } >> "+w.marker+"\nexec "+gitPath+" \"$@\"\n")
			ctx.Expect(os.Chmod(filepath.Join(shim, "git"), 0o700)).To(specs.BeNil())
			ctx.Testing().Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

			copyDir, err := w.differ.Checkout(bg, job, w.dir, w.fork)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(w.differ.Remove(bg, job, w.dir, copyDir)).To(specs.BeNil())

			data, err := os.ReadFile(w.marker)
			ctx.Expect(err).To(specs.BeNil())
			log := string(data)
			ctx.Expect(log).To(specs.Contain("worktree add"))
			ctx.Expect(log).To(specs.Contain("worktree remove"))
			ctx.Expect(log).To(specs.Not(specs.Contain(token)))
			var homes []string
			for _, line := range strings.Split(log, "\n") {
				if strings.HasPrefix(line, "HOME=") {
					homes = append(homes, line)
				}
			}
			ctx.Expect(len(homes)).ToEqual(strings.Count(log, "--- git "))
			for _, line := range homes {
				ctx.Expect(line).ToEqual("HOME=" + w.home)
			}
		})
	})
}
