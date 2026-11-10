package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/wiring"
)

// mainEnv has this test binary run factoryd's main with its arguments.
const mainEnv = "FACTORYD_TEST_MAIN"

// TestMain lets this test binary be the fake agent role-demo launches, and
// factoryd itself for the tests that run it as a process.
func TestMain(m *testing.M) {
	if acpfake.Requested() {
		os.Exit(acpfake.Run())
	}
	if os.Getenv(mainEnv) != "" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const roleDemoAgents = "agents:\n  claude:\n    command: claude-code-acp\n    env:\n      ANTHROPIC_MODEL: \"{model}\"\n  gemini:\n    command: gemini\n    args: [--experimental-acp, --model, \"{model}\"]\n"

func roleDemoConfig(ctx *specs.Context, builder string) string {
	dir := ctx.Testing().TempDir()
	ctx.Expect(os.MkdirAll(filepath.Join(dir, "roles"), 0o755)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(filepath.Join(dir, "roles.yaml"), []byte(roleDemoAgents), 0o600)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(filepath.Join(dir, "roles", "builder.md"), []byte("You are the builder.\nBuild the spec.\n"), 0o600)).To(specs.BeNil())
	setRoleDemoBuilder(ctx, dir, builder)
	return dir
}

func setRoleDemoBuilder(ctx *specs.Context, dir, agentAndModel string) {
	data := "name: builder\nkind: build\nprompt: roles/builder.md\nmode: blocking\n" + agentAndModel
	ctx.Expect(os.WriteFile(filepath.Join(dir, "roles", "builder.yaml"), []byte(data), 0o600)).To(specs.BeNil())
}

func TestRoleDemo(t *testing.T) {
	specs.Describe(t, "factoryd role-demo", func(s *specs.Spec) {
		s.It("launches the repo's builder with its agent, its model and its prompt", func(ctx *specs.Context) {
			var out bytes.Buffer

			err := roleDemo(context.Background(), []string{"--config", "../.."}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("agent claude: ANTHROPIC_MODEL=opus claude-code-acp"))
			ctx.Expect(out.String()).To(specs.Contain("model opus\n"))
			ctx.Expect(out.String()).To(specs.Contain("env [ANTHROPIC_MODEL=opus]"))
			ctx.Expect(out.String()).To(specs.Contain("   | You are the builder."))
		})

		s.It("launches the repo's security reviewer with its agent, its model and its prompt", func(ctx *specs.Context) {
			var out bytes.Buffer

			err := roleDemo(context.Background(), []string{"--config", "../..", "--role", "reviewer-security"}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("role reviewer-security (review)"))
			ctx.Expect(out.String()).To(specs.Contain("env [ANTHROPIC_MODEL=opus]"))
			ctx.Expect(out.String()).To(specs.Contain("   | You are the security reviewer."))
		})

		s.It("launches the repo's product owner with its agent, its model and its prompt", func(ctx *specs.Context) {
			var out bytes.Buffer

			err := roleDemo(context.Background(), []string{"--config", "../..", "--role", "product-owner"}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Contain("role product-owner (task)"))
			ctx.Expect(out.String()).To(specs.Contain("env [ANTHROPIC_MODEL=sonnet]"))
			ctx.Expect(out.String()).To(specs.Contain("   | You are the product owner."))
		})

		s.It("fails naming a role that does not exist, without launching anything", func(ctx *specs.Context) {
			var out bytes.Buffer

			err := roleDemo(context.Background(), []string{"--config", "../..", "--role", "no-existe"}, &out)

			ctx.Expect(err).To(specs.MatchError(wiring.ErrUnknownRole))
			ctx.Expect(err.Error()).To(specs.Contain("role no-existe"))
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})

		s.It("launches the next session with the model and agent the YAML says now", func(ctx *specs.Context) {
			dir := roleDemoConfig(ctx, "agent: claude\nmodel: opus\n")
			var first, next bytes.Buffer
			ctx.Expect(roleDemo(context.Background(), []string{"--config", dir}, &first)).To(specs.BeNil())

			setRoleDemoBuilder(ctx, dir, "agent: gemini\nmodel: gemini-3-pro\n")
			err := roleDemo(context.Background(), []string{"--config", dir}, &next)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(first.String()).To(specs.Contain("the agent was launched with args [] and env [ANTHROPIC_MODEL=opus]"))
			ctx.Expect(next.String()).To(specs.Contain("model gemini-3-pro\n"))
			ctx.Expect(next.String()).To(specs.Contain("the agent was launched with args [--experimental-acp --model gemini-3-pro] and env []"))
			ctx.Expect(next.String()).To(specs.Contain("the agent got the prompt of roles/builder.md (2 lines), starting:\n   | You are the builder.\n   | Build the spec.\n"))
		})

		s.It("fails naming an agent that is not configured, without launching anything", func(ctx *specs.Context) {
			dir := roleDemoConfig(ctx, "agent: codex\nmodel: gpt-5.5\n")
			var out bytes.Buffer

			err := roleDemo(context.Background(), []string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.MatchError(configyaml.ErrUnknownAgent))
			ctx.Expect(err.Error()).To(specs.Contain(`role builder names agent "codex"`))
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})
	})
}
