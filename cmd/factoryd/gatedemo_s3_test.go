//go:build unix

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/childenv"
)

func TestDemoGateDemoGit(t *testing.T) {
	specs.Describe(t, "factoryd gate-demo's git", func(s *specs.Spec) {
		s.It("reads the base commit with no token and the job's HOME, running no hook or filter the repo names", func(ctx *specs.Context) {
			const token = "ghp_factoryd_fake_token"
			dir := ctx.Testing().TempDir()
			ctx.Expect(os.WriteFile(filepath.Join(dir, "data.txt"), []byte("data\n"), 0o600)).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("data.txt filter=evil\n"), 0o600)).To(specs.BeNil())
			git(ctx, dir, "init", "-q", "-b", "main")
			git(ctx, dir, "add", ".")
			git(ctx, dir, "commit", "-q", "-m", "base")
			marker := filepath.Join(ctx.Testing().TempDir(), "marker")
			dump := "echo hooked > " + marker + "; true"
			git(ctx, dir, "config", "hook.evil.command", dump)
			git(ctx, dir, "config", "hook.evil.event", "post-index-change")
			git(ctx, dir, "config", "filter.evil.clean", dump+"; cat")
			// The jobs' HOMEs are under the user's cache folder.
			cache := ctx.Testing().TempDir()
			ctx.Testing().Setenv("HOME", cache)
			ctx.Testing().Setenv("XDG_CACHE_HOME", cache)
			homes, err := childenv.DefaultHomes()
			ctx.Expect(err).To(specs.BeNil())
			home, err := homes.For(gateDemoJob)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Testing().Setenv("GITHUB_TOKEN", token)
			gitPath, err := exec.LookPath("git")
			ctx.Expect(err).To(specs.BeNil())
			// From here on every git found on PATH logs its environment.
			log := filepath.Join(ctx.Testing().TempDir(), "log")
			shim := ctx.Testing().TempDir()
			script := "#!/bin/sh\n{ echo \"--- git $*\"; env; } >> " + log + "\nexec " + gitPath + " \"$@\"\n"
			ctx.Expect(os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o700)).To(specs.BeNil())
			ctx.Testing().Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			// A later mtime makes git hash data.txt again.
			ctx.Expect(os.WriteFile(filepath.Join(dir, "data.txt"), []byte("changed\n"), 0o600)).To(specs.BeNil())

			var out bytes.Buffer
			ctx.Expect(gateDemo(context.Background(), []string{"-dir", dir, "-checks", "true"}, &out)).To(specs.BeNil())

			data, err := os.ReadFile(log)
			ctx.Expect(err).To(specs.BeNil())
			ran := string(data)
			ctx.Expect(ran).To(specs.Contain("rev-parse --verify --end-of-options HEAD^{commit}"))
			ctx.Expect(ran).To(specs.Not(specs.Contain(token)))
			var homesSeen []string
			for _, line := range strings.Split(ran, "\n") {
				if strings.HasPrefix(line, "HOME=") {
					homesSeen = append(homesSeen, line)
				}
			}
			ctx.Expect(len(homesSeen)).ToEqual(strings.Count(ran, "--- git "))
			for _, line := range homesSeen {
				ctx.Expect(line).ToEqual("HOME=" + home)
			}
			_, err = os.Stat(marker)
			ctx.Expect(os.IsNotExist(err)).To(specs.BeTrue())
		})
	})
}
