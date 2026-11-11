//go:build unix

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
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// token is a credential factoryd holds, in its environment for each test.
const token = "ghp_factoryd_fake_token"

// hostile is a repo whose base has data.txt with a filter in .gitattributes,
// with a Workspace over it whose jobs' HOMEs are the test's own, job-1's
// HOME, and a marker path no file is at yet.
type hostile struct {
	repo, home, marker string
	ws                 *gitworktree.Workspace
}

const hostileJob domain.JobID = "job-1"

func newHostile(ctx *specs.Context) hostile {
	ctx.Testing().Setenv("GITHUB_TOKEN", token)
	repo := ctx.Testing().TempDir()
	must(ctx, repo, "init", "-q", "-b", "main")
	write(ctx, repo, "data.txt", "data\n")
	write(ctx, repo, ".gitattributes", "data.txt filter=evil\n")
	must(ctx, repo, "add", ".")
	must(ctx, repo, "commit", "-q", "-m", "base")
	must(ctx, repo, "branch", base)
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	homes := childenv.Homes{Root: ctx.Testing().TempDir()}
	home, err := homes.For(hostileJob)
	ctx.Expect(err).To(specs.BeNil())
	return hostile{
		repo: repo, home: home, marker: filepath.Join(ctx.Testing().TempDir(), "marker"),
		ws: gitworktree.New(repo, base, store, homes),
	}
}

func write(ctx *specs.Context, dir, name, content string) {
	ctx.Testing().Helper()
	p := filepath.Join(dir, name)
	ctx.Expect(os.MkdirAll(filepath.Dir(p), 0o700)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(p, []byte(content), 0o700)).To(specs.BeNil())
}

// dump is a shell command that writes its environment to the marker,
// whatever arguments git adds after it.
func (h hostile) dump() string {
	return "env > " + h.marker + "; true"
}

// script is a shell script at name in dir that runs dump.
func (h hostile) script(ctx *specs.Context, dir, name string) string {
	write(ctx, dir, name, "#!/bin/sh\n"+h.dump()+"\n")
	return filepath.Join(dir, name)
}

// hookEvents are the hooks git runs as it makes, backs up and removes a
// worktree.
var hookEvents = []string{"post-checkout", "reference-transaction", "post-index-change"}

func (h hostile) hooks(ctx *specs.Context, dir string) {
	for _, ev := range hookEvents {
		h.script(ctx, dir, ev)
	}
}

// configHook defines in the config file at file a hook, by name, on every
// one of hookEvents.
func (h hostile) configHook(ctx *specs.Context, file string) {
	must(ctx, h.repo, "config", "--file", file, "hook.evil.command", h.dump())
	for _, ev := range hookEvents {
		must(ctx, h.repo, "config", "--file", file, "--add", "hook.evil.event", ev)
	}
}

func (h hostile) config(ctx *specs.Context, key, value string) {
	must(ctx, h.repo, "config", key, value)
}

func (h hostile) gitDir() string {
	return filepath.Join(h.repo, ".git")
}

func (h hostile) marked() bool {
	_, err := os.Stat(h.marker)
	return err == nil
}

// prepare makes job-1's worktree, as factoryd does, and returns its path.
func (h hostile) prepare(ctx *specs.Context) string {
	wt, err := h.ws.Prepare(context.Background(), hostileJob)
	ctx.Expect(err).To(specs.BeNil())
	return wt.Path
}

// dirty changes data.txt and adds new.txt in the worktree at wt, so that
// cleaning it backs them up.
func dirty(ctx *specs.Context, wt string) {
	write(ctx, wt, "data.txt", "changed\n")
	write(ctx, wt, "new.txt", "new\n")
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

type armRow struct {
	name string
	arm  func(ctx *specs.Context, h hostile)
}

// armRows are what the builder can leave in the repo for git to run.
var armRows = []armRow{
	{"a hook in .git/hooks", func(ctx *specs.Context, h hostile) {
		h.hooks(ctx, filepath.Join(h.gitDir(), "hooks"))
	}},
	{"a hook in the folder core.hooksPath names", func(ctx *specs.Context, h hostile) {
		hooks := filepath.Join(ctx.Testing().TempDir(), "hooks")
		h.hooks(ctx, hooks)
		h.config(ctx, "core.hooksPath", hooks)
	}},
	// core.hooksPath doesn't turn these off.
	{"a hook the config defines", func(ctx *specs.Context, h hostile) {
		h.configHook(ctx, filepath.Join(h.gitDir(), "config"))
	}},
	// Only a git run on the job's branch includes it.
	{"a hook a file the config includes on the job's branch defines", func(ctx *specs.Context, h hostile) {
		h.configHook(ctx, filepath.Join(h.gitDir(), "evil.inc"))
		h.config(ctx, "includeIf.onbranch:factory/**.path", "evil.inc")
	}},
	{"a hook a file the config includes from HOME defines", func(ctx *specs.Context, h hostile) {
		h.configHook(ctx, filepath.Join(h.home, "evil.inc"))
		h.config(ctx, "include.path", "~/evil.inc")
	}},
	// git doesn't clean an include's path: link/.. is the parent of the
	// link's target, not the folder the link is in.
	{"a hook a file the config includes from HOME through a link and .. defines", func(ctx *specs.Context, h hostile) {
		h.configHook(ctx, filepath.Join(linkTarget(ctx, h.home), "evil.inc"))
		h.config(ctx, "include.path", "~/link/../evil.inc")
	}},
	{"a hook a file the config includes by a relative path through a link and .. defines", func(ctx *specs.Context, h hostile) {
		h.configHook(ctx, filepath.Join(linkTarget(ctx, h.gitDir()), "evil.inc"))
		h.config(ctx, "include.path", "link/../evil.inc")
	}},
	{"a smudge filter the branch's .gitattributes names", func(ctx *specs.Context, h hostile) {
		h.config(ctx, "filter.evil.smudge", h.dump()+"; cat")
	}},
	{"a clean filter the branch's .gitattributes names", func(ctx *specs.Context, h hostile) {
		h.config(ctx, "filter.evil.clean", h.dump()+"; cat")
	}},
	{"a required process filter the branch's .gitattributes names", func(ctx *specs.Context, h hostile) {
		h.config(ctx, "filter.evil.process", h.dump())
		h.config(ctx, "filter.evil.required", "true")
	}},
	{"a filter info/attributes names", func(ctx *specs.Context, h hostile) {
		write(ctx, h.gitDir(), "info/attributes", "*.txt filter=other\n")
		h.config(ctx, "filter.other.smudge", h.dump()+"; cat")
		h.config(ctx, "filter.other.clean", h.dump()+"; cat")
	}},
	{"an fsmonitor", func(ctx *specs.Context, h hostile) {
		h.config(ctx, "core.fsmonitor", h.script(ctx, ctx.Testing().TempDir(), "fsmonitor"))
	}},
}

func TestDemoHostileWorkspace(t *testing.T) {
	specs.Describe(t, "gitworktree.Workspace on a repo the builder set up", func(s *specs.Spec) {
		bg := context.Background()

		specs.Table(s, armRows, func(r armRow) string {
			return "makes a job's worktree, with its files as git keeps them, without running " + r.name
		}, func(ctx *specs.Context, r armRow) {
			h := newHostile(ctx)
			r.arm(ctx, h)

			wt := h.prepare(ctx)

			ctx.Expect(h.marked()).To(specs.BeFalse())
			data, err := os.ReadFile(filepath.Join(wt, "data.txt"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(string(data)).ToEqual("data\n")
		})

		specs.Table(s, armRows, func(r armRow) string {
			return "backs up and removes a job's worktree, saving its files as they are, without running " + r.name
		}, func(ctx *specs.Context, r armRow) {
			h := newHostile(ctx)
			wt := h.prepare(ctx)
			dirty(ctx, wt)
			r.arm(ctx, h)

			err := h.ws.Clean(bg, hostileJob)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(h.marked()).To(specs.BeFalse())
			ctx.Expect(exists(wt)).To(specs.BeFalse())
			ctx.Expect(must(ctx, h.repo, "--no-replace-objects", "cat-file", "blob", backupRef+":data.txt")).ToEqual("changed")
			ctx.Expect(must(ctx, h.repo, "--no-replace-objects", "cat-file", "blob", backupRef+":new.txt")).ToEqual("new")
		})

		s.It("hands no token to any git it runs to make, back up and remove a worktree, and the job's HOME to each", func(ctx *specs.Context) {
			h := newHostile(ctx)
			gitPath, err := exec.LookPath("git")
			ctx.Expect(err).To(specs.BeNil())
			// From here on every git found on PATH logs its environment.
			shim := ctx.Testing().TempDir()
			write(ctx, shim, "git", "#!/bin/sh\n{ echo \"--- git $*\"; env; } >> "+h.marker+"\nexec "+gitPath+" \"$@\"\n")
			ctx.Testing().Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

			wt := h.prepare(ctx)
			h.prepare(ctx)
			dirty(ctx, wt)
			ctx.Expect(h.ws.Clean(bg, hostileJob)).To(specs.BeNil())

			data, err := os.ReadFile(h.marker)
			ctx.Expect(err).To(specs.BeNil())
			log := string(data)
			for _, args := range []string{"rev-parse --verify", "worktree add", "rev-parse --abbrev-ref HEAD", "status --porcelain", "read-tree", "add --all", "write-tree", "commit-tree", "update-ref", "worktree remove", "branch -D"} {
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
			for _, line := range homes {
				ctx.Expect(line).ToEqual("HOME=" + h.home)
			}
		})
	})
}
