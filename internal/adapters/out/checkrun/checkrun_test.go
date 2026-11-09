package checkrun_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
	"github.com/pablogore/software-factory/internal/domain"
)

func TestRunner(t *testing.T) {
	specs.Describe(t, "checkrun.Runner", func(s *specs.Spec) {
		bg := context.Background()

		s.It("keeps the exit code, the duration and stdout and stderr of a failing command", func(ctx *specs.Context) {
			run, err := checkrun.Runner{}.Run(bg, t.TempDir(), []string{"sh", "-c", "echo out; echo err >&2; exit 3"})

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Command).ToEqual("sh -c echo out; echo err >&2; exit 3")
			ctx.Expect(run.ExitCode).ToEqual(3)
			ctx.Expect(run.Passed()).To(specs.BeFalse())
			ctx.Expect(run.Output).ToEqual("out\nerr\n")
			ctx.Expect(run.Duration > 0).To(specs.BeTrue())
		})

		s.It("runs the command in the directory it is given", func(ctx *specs.Context) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			ctx.Expect(err).To(specs.BeNil())

			run, err := checkrun.Runner{}.Run(bg, dir, []string{"pwd"})

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Passed()).To(specs.BeTrue())
			ctx.Expect(strings.TrimSpace(run.Output)).ToEqual(dir)
		})

		s.It("is an error when the command cannot start", func(ctx *specs.Context) {
			_, err := checkrun.Runner{}.Run(bg, t.TempDir(), []string{"no-such-command-for-the-gate"})

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})

		s.It("is an error when no command is given", func(ctx *specs.Context) {
			_, err := checkrun.Runner{}.Run(bg, t.TempDir(), nil)

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})

		s.It("is an error, not a failing run, when ctx ends first", func(ctx *specs.Context) {
			cancelled, cancel := context.WithCancel(bg)
			cancel()

			_, err := checkrun.Runner{}.Run(cancelled, t.TempDir(), []string{"sh", "-c", "exit 0"})

			ctx.Expect(err).To(specs.MatchError(context.Canceled))
		})
	})
}

func TestDemoRunnerEnvironment(t *testing.T) {
	specs.Describe(t, "checkrun.Runner's environment", func(s *specs.Spec) {
		s.It("runs the checks without factoryd's tokens, with PATH, the locale, TMPDIR and HOME in their directory", func(ctx *specs.Context) {
			ctx.Testing().Setenv("GITHUB_TOKEN", "ghp_secret")
			ctx.Testing().Setenv("GH_TOKEN", "gho_secret")
			ctx.Testing().Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
			ctx.Testing().Setenv("LANG", "es_AR.UTF-8")
			ctx.Testing().Setenv("LC_ALL", "es_AR.UTF-8")
			tmp := ctx.Testing().TempDir()
			ctx.Testing().Setenv("TMPDIR", tmp)
			ctx.Testing().Setenv("HOME", "/Users/operator")
			dir, err := filepath.Abs(ctx.Testing().TempDir())
			ctx.Expect(err).To(specs.BeNil())

			run, err := checkrun.Runner{}.Run(context.Background(), dir, []string{"env"})

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Passed()).To(specs.BeTrue())
			ctx.Expect(run.Output).To(specs.Not(specs.Contain("secret")))
			ctx.Expect(run.Output).To(specs.Not(specs.Contain("TOKEN")))
			ctx.Expect(run.Output).To(specs.Not(specs.Contain("ANTHROPIC_API_KEY")))
			lines := strings.Split(strings.TrimSpace(run.Output), "\n")
			ctx.Expect(lines).To(specs.ContainAllOf("PATH="+os.Getenv("PATH"), "LANG=es_AR.UTF-8", "LC_ALL=es_AR.UTF-8", "TMPDIR="+tmp, "HOME="+dir))
		})
	})
}
