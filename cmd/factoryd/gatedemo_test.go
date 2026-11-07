package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

// git runs git in dir with no user or system config.
func git(ctx *specs.Context, dir string, args ...string) {
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

func TestGateDemo(t *testing.T) {
	specs.Describe(t, "factoryd gate-demo", func(s *specs.Spec) {
		s.It("fails a job that touches files outside the build role's allowed paths, naming both, and sends it back to build", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			ctx.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme\n"), 0o600)).To(specs.BeNil())
			git(ctx, dir, "init", "-q", "-b", "main")
			git(ctx, dir, "add", ".")
			git(ctx, dir, "commit", "-q", "-m", "base")
			ctx.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o600)).To(specs.BeNil())
			ctx.Expect(os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("new\n"), 0o600)).To(specs.BeNil())
			role := filepath.Join(ctx.Testing().TempDir(), "builder.yaml")
			ctx.Expect(os.WriteFile(role, []byte("name: builder\nkind: build\nmode: blocking\nallowed_paths: [internal/slices/build/]\n"), 0o600)).To(specs.BeNil())

			var out bytes.Buffer
			err := gateDemo(context.Background(), []string{"-dir", dir, "-checks", "true", "-role", role}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("build role builder may touch only [internal/slices/build/]"))
			ctx.Expect(out.String()).To(specs.Contain("rule broken: the diff modifies README.md, outside the allowed paths of build role builder"))
			ctx.Expect(out.String()).To(specs.Contain("rule broken: the diff adds scratch.txt, outside the allowed paths of build role builder"))
			ctx.Expect(out.String()).To(specs.Contain("job gate-demo: stage build, round 2"))
		})

		s.It("refuses a role file that is not a build role", func(ctx *specs.Context) {
			role := filepath.Join(ctx.Testing().TempDir(), "reviewer.yaml")
			ctx.Expect(os.WriteFile(role, []byte("name: reviewer-code\nkind: review\nmode: blocking\n"), 0o600)).To(specs.BeNil())

			err := gateDemo(context.Background(), []string{"-checks", "true", "-role", role}, &bytes.Buffer{})

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})
	})
}
