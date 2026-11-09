package acp_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// envSeen launches the fake agent with cfg changed by edit, in a session of
// job in dir, and is the environment it got of the variables it records.
func envSeen(ctx *specs.Context, job domain.JobID, dir string, edit func(*acp.Config)) []string {
	record := recordIn(ctx)
	cfg, err := acpfake.Config(acpfake.Script{RecordEnv: []string{
		"GITHUB_TOKEN", "GH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "PATH", "LANG", "LC_ALL", "TMPDIR", "HOME", "AGENT_MODE",
	}}, record)
	ctx.Expect(err).To(specs.BeNil())
	edit(&cfg)

	sess, err := acp.New(cfg, stopped{}).Start(context.Background(), ports.SessionSpec{Job: job, Role: role, Dir: dir})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Cleanup(func() { _ = sess.Close() })
	got, err := acpfake.Load(record)
	ctx.Expect(err).To(specs.BeNil())
	return got.Env
}

// homeIn is the HOME of env.
func homeIn(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			return v
		}
	}
	return ""
}

func TestDemoAgentEnvironment(t *testing.T) {
	specs.Describe(t, "acp.Agent's environment", func(s *specs.Spec) {
		s.It("launches the agent without factoryd's tokens, with the variables its adapter declares and passes, PATH, the locale, TMPDIR and the job's HOME outside its worktree", func(ctx *specs.Context) {
			ctx.Testing().Setenv("GITHUB_TOKEN", "ghp_secret")
			ctx.Testing().Setenv("GH_TOKEN", "gho_secret")
			ctx.Testing().Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
			ctx.Testing().Setenv("OPENAI_API_KEY", "sk-openai-secret")
			ctx.Testing().Setenv("PATH", "/usr/bin:/bin")
			ctx.Testing().Setenv("LANG", "es_AR.UTF-8")
			ctx.Testing().Setenv("LC_ALL", "es_AR.UTF-8")
			tmp := ctx.Testing().TempDir()
			ctx.Testing().Setenv("TMPDIR", tmp)
			ctx.Testing().Setenv("HOME", "/Users/operator")
			dir, err := filepath.Abs(ctx.Testing().TempDir())
			ctx.Expect(err).To(specs.BeNil())
			var root string

			got := envSeen(ctx, jobID, dir, func(cfg *acp.Config) {
				cfg.Env = append(cfg.Env, "AGENT_MODE=acp")
				// GEMINI_API_KEY is not in factoryd's environment.
				cfg.PassEnv = []string{"ANTHROPIC_API_KEY", "GEMINI_API_KEY"}
				root = cfg.Homes.Root
			})

			ctx.Expect(got).To(specs.ContainTheSameElementsAs([]string{
				"PATH=/usr/bin:/bin", "LANG=es_AR.UTF-8", "LC_ALL=es_AR.UTF-8", "TMPDIR=" + tmp, "HOME=" + filepath.Join(root, string(jobID)), "AGENT_MODE=acp", "ANTHROPIC_API_KEY=sk-ant-secret",
			}))
			ctx.Expect(strings.HasPrefix(homeIn(got), dir)).To(specs.BeFalse())
		})

		s.It("gives the agents of two jobs two HOMEs", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			root := ctx.Testing().TempDir()
			var homes []string
			for _, job := range []domain.JobID{"j-1", "j-2"} {
				homes = append(homes, homeIn(envSeen(ctx, job, dir, func(cfg *acp.Config) { cfg.Homes.Root = root })))
			}

			ctx.Expect(homes).ToEqual([]string{filepath.Join(root, "j-1"), filepath.Join(root, "j-2")})
		})

		s.It("launches an agent with home: user with the operator's HOME", func(ctx *specs.Context) {
			ctx.Testing().Setenv("HOME", "/Users/operator")

			got := envSeen(ctx, jobID, ctx.Testing().TempDir(), func(cfg *acp.Config) { cfg.UserHome = true })

			ctx.Expect(homeIn(got)).ToEqual("/Users/operator")
		})

		s.It("refuses to start a session with no root for the jobs' HOMEs", func(ctx *specs.Context) {
			a := acp.New(acp.Config{Command: "true"}, stopped{})

			_, err := a.Start(context.Background(), ports.SessionSpec{Job: jobID, Role: role, Dir: ctx.Testing().TempDir()})

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
