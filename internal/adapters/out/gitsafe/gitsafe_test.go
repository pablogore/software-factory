package gitsafe_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/gitsafe"
)

func git(ctx *specs.Context, dir string, args ...string) string {
	ctx.Testing().Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	ctx.Expect(err).To(specs.BeNil())
	return strings.TrimSpace(string(out))
}

func TestRun(t *testing.T) {
	specs.Describe(t, "gitsafe.Run", func(s *specs.Spec) {
		// git resolves ~name with getpwnam, which can find a user
		// user.Lookup can't, and would read the file and run its hooks. The
		// include is on a branch the repo isn't on, so the git run here
		// doesn't expand it but one in a job's worktree would.
		s.It("fails and runs no git command when the config includes a file from the home of a user it can't resolve", func(ctx *specs.Context) {
			repo := ctx.Testing().TempDir()
			git(ctx, repo, "init", "-q", "-b", "main")
			git(ctx, repo, "commit", "-q", "--allow-empty", "-m", "base")
			git(ctx, repo, "config", "includeIf.onbranch:factory/**.path", "~nosuchuser-xyz/x.inc")
			gitPath, err := exec.LookPath("git")
			ctx.Expect(err).To(specs.BeNil())
			// From here on every git found on PATH logs its arguments.
			shim, log := ctx.Testing().TempDir(), filepath.Join(ctx.Testing().TempDir(), "log")
			script := "#!/bin/sh\necho \"git $*\" >> " + log + "\nexec " + gitPath + " \"$@\"\n"
			ctx.Expect(os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o700)).To(specs.BeNil())
			ctx.Testing().Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

			_, err = gitsafe.Run(context.Background(), ctx.Testing().TempDir(), repo, nil, "tag", "ran")

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("gitsafe: cannot resolve include path ~nosuchuser-xyz"))
			data, err := os.ReadFile(log)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(string(data)).To(specs.Not(specs.Contain("git tag ran")))
		})
	})
}
