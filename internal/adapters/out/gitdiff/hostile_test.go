//go:build unix

package gitdiff_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// token is a credential factoryd holds, in its environment for each test.
const token = "ghp_factoryd_fake_token"

// hostile is a repo whose tracked data.txt the builder touched, so that git
// diff hashes it again and rewrites the index, with a Differ whose jobs'
// HOMEs are the test's own, job's HOME in it, and a marker path no file is
// at yet.
type hostile struct {
	dir, fork, home, marker string
	differ                  gitdiff.Differ
}

func newHostile(ctx *specs.Context) hostile {
	ctx.Testing().Setenv("GITHUB_TOKEN", token)
	dir, fork := newRepoWith(ctx, map[string]string{"data.txt": "data\n"})
	homes := childenv.Homes{Root: ctx.Testing().TempDir()}
	home, err := homes.For(job)
	ctx.Expect(err).To(specs.BeNil())
	// A later mtime makes the index's stat data stale without a change.
	later := time.Now().Add(time.Hour)
	ctx.Expect(os.Chtimes(filepath.Join(dir, "data.txt"), later, later)).To(specs.BeNil())
	return hostile{dir: dir, fork: fork, home: home, marker: filepath.Join(ctx.Testing().TempDir(), "marker"), differ: gitdiff.Differ{Homes: homes}}
}

// dump is a shell command that writes its environment to the marker,
// whatever arguments git adds after it.
func (h hostile) dump() string {
	return "env > " + h.marker + "; true"
}

// hook writes a hook script named name into folder that runs dump.
func (h hostile) hook(ctx *specs.Context, folder, name string) {
	ctx.Expect(os.MkdirAll(folder, 0o700)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(filepath.Join(folder, name), []byte("#!/bin/sh\n"+h.dump()+"\n"), 0o700)).To(specs.BeNil())
}

// changes has the Differ list the job's changes, as the gate does.
func (h hostile) changes(ctx *specs.Context) {
	_, err := h.differ.Changes(context.Background(), job, h.dir, h.fork)
	ctx.Expect(err).To(specs.BeNil())
}

// changesFailing is changes for a repo that makes git diff fail.
func (h hostile) changesFailing(ctx *specs.Context) {
	_, err := h.differ.Changes(context.Background(), job, h.dir, h.fork)
	ctx.Expect(err).To(specs.Not(specs.BeNil()))
}

func (h hostile) marked() bool {
	_, err := os.Stat(h.marker)
	return err == nil
}

// markerEnv is the environment the marker recorded.
func (h hostile) markerEnv(ctx *specs.Context) string {
	data, err := os.ReadFile(h.marker)
	ctx.Expect(err).To(specs.BeNil())
	return string(data)
}

// expectJobEnv checks the marker recorded the job's HOME and no token.
func (h hostile) expectJobEnv(ctx *specs.Context) {
	env := h.markerEnv(ctx)
	ctx.Expect(env).To(specs.Not(specs.Contain(token)))
	ctx.Expect(strings.Split(env, "\n")).To(specs.Contain("HOME=" + h.home))
}

func TestDemoHostileRepo(t *testing.T) {
	specs.Describe(t, "gitdiff.Differ on a repo the builder set up", func(s *specs.Spec) {
		bg := context.Background()

		s.It("runs no hook from .git/hooks when git diff rewrites the index", func(ctx *specs.Context) {
			h := newHostile(ctx)
			h.hook(ctx, filepath.Join(h.dir, ".git", "hooks"), "post-index-change")

			h.changes(ctx)

			ctx.Expect(h.marked()).To(specs.BeFalse())
		})

		s.It("runs no hook from the folder core.hooksPath names", func(ctx *specs.Context) {
			h := newHostile(ctx)
			hooks := filepath.Join(ctx.Testing().TempDir(), "hooks")
			h.hook(ctx, hooks, "post-index-change")
			must(ctx, h.dir, "config", "core.hooksPath", hooks)

			h.changes(ctx)

			ctx.Expect(h.marked()).To(specs.BeFalse())
		})

		// core.hooksPath doesn't turn these off: they run, but get the
		// job's environment, not factoryd's.
		s.It("runs a hook the config defines with the job's HOME and no token", func(ctx *specs.Context) {
			h := newHostile(ctx)
			must(ctx, h.dir, "config", "hook.evil.command", h.dump())
			must(ctx, h.dir, "config", "hook.evil.event", "post-index-change")

			h.changes(ctx)

			h.expectJobEnv(ctx)
		})

		s.It("runs a clean filter with the job's HOME and no token", func(ctx *specs.Context) {
			h := newHostile(ctx)
			write(ctx, h.dir, ".git/info/attributes", "data.txt filter=evil\n")
			must(ctx, h.dir, "config", "filter.evil.clean", h.dump()+"; cat")

			h.changes(ctx)

			h.expectJobEnv(ctx)
		})

		s.It("runs a process filter with the job's HOME and no token", func(ctx *specs.Context) {
			h := newHostile(ctx)
			write(ctx, h.dir, ".git/info/attributes", "data.txt filter=evil\n")
			must(ctx, h.dir, "config", "filter.evil.process", h.dump())

			// git gives up once the filter quits without its handshake.
			h.changesFailing(ctx)

			h.expectJobEnv(ctx)
		})

		s.It("fetches no object missing from the store from a promisor remote", func(ctx *specs.Context) {
			h := newHostile(ctx)
			blob := must(ctx, h.dir, "rev-parse", h.fork+":README.md")
			ctx.Expect(os.Remove(filepath.Join(h.dir, ".git", "objects", blob[:2], blob[2:]))).To(specs.BeNil())
			must(ctx, h.dir, "config", "core.repositoryFormatVersion", "1")
			must(ctx, h.dir, "config", "extensions.partialClone", "origin")
			must(ctx, h.dir, "config", "remote.origin.promisor", "true")
			must(ctx, h.dir, "config", "protocol.ext.allow", "always")
			// In an ext:: URL "% " is a space in an argument.
			must(ctx, h.dir, "config", "remote.origin.url", "ext::sh -c env% >% "+h.marker)

			_, err := h.differ.AtBase(bg, job, h.dir, h.fork, "README.md")

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(h.marked()).To(specs.BeFalse())
		})

		// The job's HOME is the builder's to write, as its checks share it.
		s.It("lists an untracked file a .gitconfig in the job's HOME points the scratch repo's ignore rules away from", func(ctx *specs.Context) {
			h := newHostile(ctx)
			must(ctx, h.dir, "switch", "-q", base)
			write(ctx, h.dir, ".gitignore", "*.log\n")
			must(ctx, h.dir, "add", ".gitignore")
			must(ctx, h.dir, "commit", "-q", "-m", "ignore")
			fork := must(ctx, h.dir, "rev-parse", "HEAD")
			must(ctx, h.dir, "switch", "-q", "factory/job-1")
			// git init copies the template's config, and core.worktree in it
			// would make check-ignore read the job's .gitignore files.
			templates := ctx.Testing().TempDir()
			write(ctx, templates, "config", "[core]\n\tworktree = "+h.dir+"\n")
			write(ctx, h.home, ".gitconfig", "[init]\n\ttemplateDir = "+templates+"\n")
			write(ctx, h.dir, "sub/.gitignore", "*\n")
			write(ctx, h.dir, "sub/evil", "evil\n")

			changes, err := h.differ.Changes(bg, job, h.dir, fork)

			ctx.Expect(err).To(specs.BeNil())
			var paths []string
			for _, c := range changes {
				paths = append(paths, c.Path)
			}
			ctx.Expect(paths).To(specs.ContainAllOf("sub/.gitignore", "sub/evil"))
		})

		// A filter the repo's own attributes name still runs (S1c), but the
		// job's HOME adds none.
		s.It("reads no attributes file from the job's HOME", func(ctx *specs.Context) {
			h := newHostile(ctx)
			write(ctx, h.home, ".config/git/attributes", "data.txt filter=evil\n")
			must(ctx, h.dir, "config", "filter.evil.clean", h.dump()+"; cat")

			h.changes(ctx)

			ctx.Expect(h.marked()).To(specs.BeFalse())
		})

		s.It("hands no token to any git it runs, and the job's HOME to each", func(ctx *specs.Context) {
			h := newHostile(ctx)
			gitPath, err := exec.LookPath("git")
			ctx.Expect(err).To(specs.BeNil())
			// A .gitignore at the base and an untracked file make Changes
			// check the file in a scratch repo too.
			write(ctx, h.dir, "new.txt", "new\n")
			must(ctx, h.dir, "switch", "-q", base)
			write(ctx, h.dir, ".gitignore", "*.log\n")
			must(ctx, h.dir, "add", ".gitignore")
			must(ctx, h.dir, "commit", "-q", "-m", "ignore")
			fork := must(ctx, h.dir, "rev-parse", "HEAD")
			must(ctx, h.dir, "switch", "-q", "factory/job-1")
			// From here on every git found on PATH logs its environment.
			shim := ctx.Testing().TempDir()
			script := "#!/bin/sh\n{ echo \"--- git $*\"; env; } >> " + h.marker + "\nexec " + gitPath + " \"$@\"\n"
			ctx.Expect(os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o700)).To(specs.BeNil())
			ctx.Testing().Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

			_, err = h.differ.Flagged(bg, job, h.dir)
			ctx.Expect(err).To(specs.BeNil())
			_, err = h.differ.Changes(bg, job, h.dir, fork)
			ctx.Expect(err).To(specs.BeNil())
			_, err = h.differ.AtBase(bg, job, h.dir, fork, "README.md")
			ctx.Expect(err).To(specs.BeNil())
			_, _, err = h.differ.AtBaseIfAny(bg, job, h.dir, fork, ".factory/gates.yaml")
			ctx.Expect(err).To(specs.BeNil())
			_, err = h.differ.InWorktree(bg, job, h.dir, "README.md")
			ctx.Expect(err).To(specs.BeNil())
			copyDir, err := h.differ.Checkout(bg, job, h.dir, fork)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(h.differ.Remove(bg, job, h.dir, copyDir)).To(specs.BeNil())

			log := h.markerEnv(ctx)
			for _, args := range []string{"ls-files -v", "diff --raw", "check-ignore", "cat-file blob", "ls-tree", "rev-parse --show-toplevel", "worktree add", "worktree remove"} {
				ctx.Expect(log).To(specs.Contain(args))
			}
			ctx.Expect(log).To(specs.Not(specs.Contain(token)))
			var homes []string
			for _, line := range strings.Split(log, "\n") {
				if strings.HasPrefix(line, "HOME=") {
					homes = append(homes, line)
				}
			}
			ctx.Expect(len(homes)).ToEqual(strings.Count(log, "--- git "))
			ctx.Expect(strings.Count(log, "\nGIT_CONFIG_GLOBAL="+os.DevNull+"\n")).ToEqual(len(homes))
			ctx.Expect(strings.Count(log, "\nGIT_CONFIG_NOSYSTEM=1\n")).ToEqual(len(homes))
			for _, line := range homes {
				ctx.Expect(line).ToEqual("HOME=" + h.home)
			}
		})
	})
}
