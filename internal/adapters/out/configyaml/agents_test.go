package configyaml_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

const agentsYAML = `agents:
  claude:
    command: claude-code-acp
    env:
      ANTHROPIC_MODEL: "{model}"
      DISABLE_TELEMETRY: "1"
  gemini:
    command: gemini
    args: [--experimental-acp, --model, "{model}"]
`

type badAgentsRow struct {
	name     string
	yaml     string
	sentinel error
	want     string
}

type badRoleAgentRow struct {
	name     string
	role     domain.Role
	sentinel error
	want     string
}

func TestLoadAgents(t *testing.T) {
	specs.Describe(t, "LoadAgents", func(s *specs.Spec) {
		s.It("loads each agent's command, args and env sorted by name", func(ctx *specs.Context) {
			agents, err := configyaml.LoadAgents([]byte(agentsYAML))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(agents).ToEqual(configyaml.Agents{
				"claude": {Command: "claude-code-acp", Env: []string{"ANTHROPIC_MODEL={model}", "DISABLE_TELEMETRY=1"}},
				"gemini": {Command: "gemini", Args: []string{"--experimental-acp", "--model", "{model}"}},
			})
		})

		s.When("roles.yaml is wrong", func(s *specs.Spec) {
			rows := []badAgentsRow{
				{"rejects a file with no agents", "agents: {}\n", domain.ErrMissingField, "roles.yaml: missing field: agents"},
				{"names an agent without a command", "agents:\n  codex:\n    args: [\"{model}\"]\n", domain.ErrMissingField, "roles.yaml: agent codex: missing field: command"},
				{
					"names an agent that would never get a role's model",
					"agents:\n  codex:\n    command: codex-acp\n",
					domain.ErrInvalidValue,
					"roles.yaml: agent codex: invalid value: no {model} in its args or env, so it would never get a role's model",
				},
				{"names an unknown key inside an agent", "agents:\n  codex:\n    command: codex-acp\n    timeout: 5\n", domain.ErrInvalidValue, `line 4: unknown key "timeout"`},
			}
			specs.Table(s, rows, func(r badAgentsRow) string { return r.name }, func(ctx *specs.Context, r badAgentsRow) {
				_, err := configyaml.LoadAgents([]byte(r.yaml))

				ctx.Expect(err).To(specs.MatchError(r.sentinel))
				ctx.Expect(err.Error()).To(specs.Contain(r.want))
			})
		})
	})
}

func TestAgentsFor(t *testing.T) {
	specs.Describe(t, "Agents.For", func(s *specs.Spec) {
		s.It("fills the role's model into the agent's env", func(ctx *specs.Context) {
			agents, err := configyaml.LoadAgents([]byte(agentsYAML))
			ctx.Expect(err).To(specs.BeNil())

			a, err := agents.For(domain.Role{Name: "builder", Agent: "claude", Model: "opus"})

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(a).ToEqual(configyaml.Agent{Command: "claude-code-acp", Env: []string{"ANTHROPIC_MODEL=opus", "DISABLE_TELEMETRY=1"}})
		})

		s.It("fills the role's model into the agent's args", func(ctx *specs.Context) {
			agents, err := configyaml.LoadAgents([]byte(agentsYAML))
			ctx.Expect(err).To(specs.BeNil())

			a, err := agents.For(domain.Role{Name: "builder", Agent: "gemini", Model: "gemini-3-pro"})

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(a).ToEqual(configyaml.Agent{Command: "gemini", Args: []string{"--experimental-acp", "--model", "gemini-3-pro"}})
		})

		s.When("the role cannot be launched", func(s *specs.Spec) {
			rows := []badRoleAgentRow{
				{"names the agent roles.yaml does not configure", domain.Role{Name: "builder", Agent: "codex", Model: "gpt-5.5"}, configyaml.ErrUnknownAgent, `role builder names agent "codex": agent not configured in roles.yaml`},
				{"names a role with no agent", domain.Role{Name: "builder", Model: "opus"}, domain.ErrMissingField, "role builder: missing field: agent"},
				{"names a role with no model", domain.Role{Name: "builder", Agent: "claude"}, domain.ErrMissingField, "role builder: missing field: model"},
			}
			specs.Table(s, rows, func(r badRoleAgentRow) string { return r.name }, func(ctx *specs.Context, r badRoleAgentRow) {
				agents, err := configyaml.LoadAgents([]byte(agentsYAML))
				ctx.Expect(err).To(specs.BeNil())

				_, err = agents.For(r.role)

				ctx.Expect(err).To(specs.MatchError(r.sentinel))
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})
	})
}
