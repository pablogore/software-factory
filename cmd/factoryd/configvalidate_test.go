package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/wiring"
)

// configValidateRoot is a configuration root with a builder on opus and a
// reviewer on reviewer's agent and model.
func configValidateRoot(ctx *specs.Context, reviewer string) string {
	dir := roleDemoConfig(ctx, "agent: claude\nmodel: opus\n")
	ctx.Expect(os.WriteFile(filepath.Join(dir, "roles", "reviewer.md"), []byte("Review the branch.\n"), 0o600)).To(specs.BeNil())
	data := "name: reviewer\nkind: review\nprompt: roles/reviewer.md\nmode: blocking\n" + reviewer
	ctx.Expect(os.WriteFile(filepath.Join(dir, "roles", "reviewer.yaml"), []byte(data), 0o600)).To(specs.BeNil())
	return dir
}

func TestConfigValidate(t *testing.T) {
	specs.Describe(t, "factoryd config-validate", func(s *specs.Spec) {
		s.It("warns that the repo's security reviewer uses the builder's agent and model, and succeeds", func(ctx *specs.Context) {
			var out bytes.Buffer

			err := configValidate([]string{"--config", "../.."}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).ToEqual("warning: reviewer-security uses the same agent and model as builder: claude opus\nconfiguration in ../.. is valid, with warnings\n")
		})

		s.It("says the configuration is valid, with no warnings, when no reviewer matches the builder", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: claude\nmodel: sonnet\n")
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).ToEqual("configuration in " + dir + " is valid\n")
		})

		s.It("does not warn about a reviewer on the builder's model but another agent", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: gemini\nmodel: opus\n")
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Not(specs.Contain("warning")))
		})

		s.It("fails on a root with no roles instead of calling it valid", func(ctx *specs.Context) {
			dir := filepath.Join(ctx.Testing().TempDir(), "no-existe")
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.MatchError(wiring.ErrNoRoles))
			ctx.Expect(err.Error()).To(specs.Contain(dir))
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})

		s.It("fails with role-demo's error when a role names an agent that is not configured", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: nadie\nmodel: opus\n")
			var out, demo bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)
			demoErr := roleDemo(context.Background(), []string{"--config", dir, "--role", "reviewer"}, &demo)

			ctx.Expect(err).To(specs.MatchError(configyaml.ErrUnknownAgent))
			ctx.Expect(err.Error()).To(specs.Contain(`"nadie"`))
			ctx.Expect(err.Error()).ToEqual(demoErr.Error())
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})
	})
}

// TestDemoConfigValidatePipelines is the S1 demo: the repo's pipelines load
// with its roles, and a pipeline naming a role with no file fails the command.
func TestDemoConfigValidatePipelines(t *testing.T) {
	specs.Describe(t, "factoryd config-validate on pipelines", func(s *specs.Spec) {
		s.It("succeeds on the repo, whose pipelines name only its roles", func(ctx *specs.Context) {
			var out bytes.Buffer

			err := configValidate([]string{"--config", "../.."}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("configuration in ../.. is valid"))
		})

		s.It("fails naming the pipeline and the role when a pipeline names a role with no file", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: claude\nmodel: sonnet\n")
			ctx.Expect(os.MkdirAll(filepath.Join(dir, "pipelines"), 0o755)).To(specs.BeNil())
			data := "name: default\nstages:\n  - build\n  - review:\n      parallel: [reviewer, security-deep]\n      on_changes: build\n  - approve\n"
			ctx.Expect(os.WriteFile(filepath.Join(dir, "pipelines", "default.yaml"), []byte(data), 0o600)).To(specs.BeNil())
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).To(specs.Contain("pipelines/default.yaml"))
			ctx.Expect(err.Error()).To(specs.Contain(`role "security-deep" is not loaded`))
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})

		s.It("still succeeds with a warning when the pipelines load", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: claude\nmodel: opus\n")
			ctx.Expect(os.MkdirAll(filepath.Join(dir, "pipelines"), 0o755)).To(specs.BeNil())
			data := "name: default\nstages:\n  - build\n  - review:\n      parallel: [reviewer]\n      on_changes: build\n  - approve\n"
			ctx.Expect(os.WriteFile(filepath.Join(dir, "pipelines", "default.yaml"), []byte(data), 0o600)).To(specs.BeNil())
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).ToEqual("warning: reviewer uses the same agent and model as builder: claude opus\nconfiguration in " + dir + " is valid, with warnings\n")
		})
	})
}

type withheldPassRow struct{ name string }

// TestDemoConfigValidateAgentEnv is the S2 demo: an agent's env_passthrough
// may not name factoryd's tokens, and an agent getting ANTHROPIC_API_KEY is
// warned about, as it bills by API.
func TestDemoConfigValidateAgentEnv(t *testing.T) {
	specs.Describe(t, "factoryd config-validate on the agents' environment", func(s *specs.Spec) {
		rows := []withheldPassRow{{"GITHUB_TOKEN"}, {"GH_TOKEN"}, {"DISCORD_BOT_TOKEN"}}
		specs.Table(s, rows, func(r withheldPassRow) string { return "fails naming " + r.name + " in an agent's env_passthrough" }, func(ctx *specs.Context, r withheldPassRow) {
			dir := configValidateRoot(ctx, "agent: claude\nmodel: sonnet\n")
			agents := "agents:\n  claude:\n    command: claude-code-acp\n    env_passthrough: [" + r.name + "]\n    env:\n      ANTHROPIC_MODEL: \"{model}\"\n"
			ctx.Expect(os.WriteFile(filepath.Join(dir, "roles.yaml"), []byte(agents), 0o600)).To(specs.BeNil())
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).To(specs.Contain("agent claude"))
			ctx.Expect(err.Error()).To(specs.Contain("env_passthrough names " + r.name))
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})

		s.It("warns that claude bills by API when it gets ANTHROPIC_API_KEY, and succeeds", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: claude\nmodel: sonnet\n")
			agents := "agents:\n  claude:\n    command: claude-code-acp\n    env_passthrough: [ANTHROPIC_API_KEY]\n    env:\n      ANTHROPIC_MODEL: \"{model}\"\n"
			ctx.Expect(os.WriteFile(filepath.Join(dir, "roles.yaml"), []byte(agents), 0o600)).To(specs.BeNil())
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).ToEqual("warning: agent claude gets ANTHROPIC_API_KEY: it will bill by API, not by your subscription\nconfiguration in " + dir + " is valid, with warnings\n")
		})

		s.It("has the repo's claude log in with the operator's home and no API key", func(ctx *specs.Context) {
			data, err := os.ReadFile("../../roles.yaml")
			ctx.Expect(err).To(specs.BeNil())
			agents, err := configyaml.LoadAgents(data)
			ctx.Expect(err).To(specs.BeNil())
			var out bytes.Buffer

			err = configValidate([]string{"--config", "../.."}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(agents["claude"].UserHome).To(specs.BeTrue())
			ctx.Expect(agents["claude"].PassEnv).To(specs.BeEmpty())
			ctx.Expect(out.String()).To(specs.Not(specs.Contain("bill by API")))
		})
	})
}
