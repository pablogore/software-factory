package acp_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/ports"
)

func TestDemoAgentEnvironment(t *testing.T) {
	specs.Describe(t, "acp.Agent's environment", func(s *specs.Spec) {
		s.It("launches the agent without factoryd's tokens, with the variables its adapter declares, PATH, the locale, TMPDIR and HOME in the worktree", func(ctx *specs.Context) {
			ctx.Testing().Setenv("GITHUB_TOKEN", "ghp_secret")
			ctx.Testing().Setenv("GH_TOKEN", "gho_secret")
			ctx.Testing().Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
			ctx.Testing().Setenv("PATH", "/usr/bin:/bin")
			ctx.Testing().Setenv("LANG", "es_AR.UTF-8")
			ctx.Testing().Setenv("LC_ALL", "es_AR.UTF-8")
			tmp := ctx.Testing().TempDir()
			ctx.Testing().Setenv("TMPDIR", tmp)
			ctx.Testing().Setenv("HOME", "/Users/operator")
			record := recordIn(ctx)
			cfg, err := acpfake.Config(acpfake.Script{RecordEnv: []string{
				"GITHUB_TOKEN", "GH_TOKEN", "ANTHROPIC_API_KEY", "PATH", "LANG", "LC_ALL", "TMPDIR", "HOME", "AGENT_MODE",
			}}, record)
			ctx.Expect(err).To(specs.BeNil())
			cfg.Env = append(cfg.Env, "AGENT_MODE=acp")
			dir, err := filepath.Abs(ctx.Testing().TempDir())
			ctx.Expect(err).To(specs.BeNil())

			sess, err := acp.New(cfg, stopped{}).Start(context.Background(), ports.SessionSpec{Job: jobID, Role: role, Dir: dir})
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = sess.Close() })
			got, err := acpfake.Load(record)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got.Env).To(specs.ContainTheSameElementsAs([]string{
				"PATH=/usr/bin:/bin", "LANG=es_AR.UTF-8", "LC_ALL=es_AR.UTF-8", "TMPDIR=" + tmp, "HOME=" + dir, "AGENT_MODE=acp",
			}))
		})
	})
}
