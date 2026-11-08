package wiring_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/wiring"
)

// repoRoot is the configuration this repo ships: roles.yaml and roles/.
const repoRoot = "../.."

// configRoot writes a configuration root with a builder of agent and model.
func configRoot(ctx *specs.Context, agent, model string) string {
	dir := ctx.Testing().TempDir()
	write(ctx, dir, "roles.yaml", "agents:\n  claude:\n    command: claude-code-acp\n    env:\n      ANTHROPIC_MODEL: \"{model}\"\n  gemini:\n    command: gemini\n    args: [--experimental-acp, --model, \"{model}\"]\n")
	write(ctx, dir, "roles/builder.md", "Build the spec.\n")
	setBuilder(ctx, dir, agent, model)
	return dir
}

func setBuilder(ctx *specs.Context, dir, agent, model string) {
	write(ctx, dir, "roles/builder.yaml", "name: builder\nkind: build\nprompt: roles/builder.md\nmode: blocking\nagent: "+agent+"\nmodel: "+model+"\n")
}

func write(ctx *specs.Context, dir, name, data string) {
	path := filepath.Join(dir, filepath.FromSlash(name))
	ctx.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(path, []byte(data), 0o600)).To(specs.BeNil())
}

type repoRole struct {
	name  string
	kind  domain.Kind
	model string
}

// repoRoles are the ten roles of main's .claude/agents, each with the model
// it runs on there; the reviewers with the one main's routing gives them.
var repoRoles = []repoRole{
	{"intake", domain.KindIntake, "sonnet"},
	{"spec-writer", domain.KindSpec, "opus"},
	{"builder", domain.KindBuild, "opus"},
	{"reviewer-code", domain.KindReview, "sonnet"},
	{"reviewer-security", domain.KindReview, "opus"},
	{"reviewer-ux", domain.KindReview, "sonnet"},
	{"reviewer-ui", domain.KindReview, "haiku"},
	{"approver", domain.KindApprove, "opus"},
	{"tech-lead", domain.KindTriage, "sonnet"},
	{"product-owner", domain.KindTask, "sonnet"},
}

// protocolWords belong to how factoryd runs a role, not to what the role
// does, so no prompt may carry them.
var protocolWords = []string{"build.md", "submit_result", "factory/", ".worktrees", "job folder", "round-", "git push", "gh pr", "STATUS:", "SendMessage", "VERDICT:", "DECISION:", "config.json"}

func TestLoadRoleSession(t *testing.T) {
	specs.Describe(t, "LoadRoleSession", func(s *specs.Spec) {
		s.It("starts the repo's builder with its agent, its model and roles/builder.md", func(ctx *specs.Context) {
			prompt, err := os.ReadFile(filepath.Join(repoRoot, "roles", "builder.md"))
			ctx.Expect(err).To(specs.BeNil())

			rs, err := wiring.LoadRoleSession(repoRoot, "builder")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(rs.Role.Kind).ToEqual(domain.KindBuild)
			ctx.Expect(rs.Role.Agent).ToEqual("claude")
			ctx.Expect(rs.Role.Model).ToEqual("opus")
			ctx.Expect(rs.Agent).ToEqual(acp.Config{Command: "claude-code-acp", Env: []string{"ANTHROPIC_MODEL=opus"}})
			ctx.Expect(rs.Prompt).ToEqual(string(prompt))
		})

		specs.Table(s, repoRoles, func(r repoRole) string {
			return "starts the repo's " + r.name + " with its agent, its model and a short prompt free of protocol, paths and state"
		}, func(ctx *specs.Context, r repoRole) {
			prompt, err := os.ReadFile(filepath.Join(repoRoot, "roles", r.name+".md"))
			ctx.Expect(err).To(specs.BeNil())

			rs, err := wiring.LoadRoleSession(repoRoot, r.name)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(rs.Role.Kind).ToEqual(r.kind)
			ctx.Expect(rs.Role.Agent).ToEqual("claude")
			ctx.Expect(rs.Role.Model).ToEqual(r.model)
			ctx.Expect(rs.Agent).ToEqual(acp.Config{Command: "claude-code-acp", Env: []string{"ANTHROPIC_MODEL=" + r.model}})
			ctx.Expect(rs.Prompt).ToEqual(string(prompt))
			ctx.Expect(strings.Count(rs.Prompt, "\n")).To(specs.BeLessThanOrEqual(60))
			for _, word := range protocolWords {
				ctx.Expect(rs.Prompt).To(specs.Not(specs.Contain(word)))
			}
		})

		s.It("fails naming a role that has no file", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")

			_, err := wiring.LoadRoleSession(dir, "no-existe")

			ctx.Expect(err).To(specs.MatchError(wiring.ErrUnknownRole))
			ctx.Expect(err.Error()).To(specs.Contain("role no-existe"))
		})

		s.It("fails on a role name that would reach outside roles/", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			write(ctx, dir, "builder.yaml", "name: builder\nkind: build\nprompt: roles/builder.md\nmode: blocking\nagent: claude\nmodel: opus\n")

			_, err := wiring.LoadRoleSession(dir, "../builder")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).To(specs.Contain(`"../builder"`))
		})

		s.It("gives the next session the agent and model the YAML says now", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			first, err := wiring.LoadRoleSession(dir, "builder")
			ctx.Expect(err).To(specs.BeNil())

			setBuilder(ctx, dir, "gemini", "gemini-3-pro")
			next, err := wiring.LoadRoleSession(dir, "builder")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(first.Agent).ToEqual(acp.Config{Command: "claude-code-acp", Env: []string{"ANTHROPIC_MODEL=opus"}})
			ctx.Expect(next.Agent).ToEqual(acp.Config{Command: "gemini", Args: []string{"--experimental-acp", "--model", "gemini-3-pro"}})
		})

		s.It("fails naming an agent roles.yaml does not configure", func(ctx *specs.Context) {
			dir := configRoot(ctx, "codex", "gpt-5.5")

			_, err := wiring.LoadRoleSession(dir, "builder")

			ctx.Expect(err).To(specs.MatchError(configyaml.ErrUnknownAgent))
			ctx.Expect(err.Error()).To(specs.Contain(`agent "codex"`))
		})

		s.It("fails on a role file named for another role", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			write(ctx, dir, "roles/approver.yaml", "name: builder\nkind: build\nprompt: roles/builder.md\nmode: blocking\nagent: claude\nmodel: opus\n")

			_, err := wiring.LoadRoleSession(dir, "approver")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("fails on an empty prompt file", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			write(ctx, dir, "roles/builder.md", "\n")

			_, err := wiring.LoadRoleSession(dir, "builder")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).To(specs.Contain("roles/builder.md is empty"))
		})
	})
}
