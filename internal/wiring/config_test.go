package wiring_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/wiring"
)

func setReviewer(ctx *specs.Context, dir, agent, model string) {
	write(ctx, dir, "roles/reviewer.md", "Review the branch.\n")
	write(ctx, dir, "roles/reviewer.yaml", "name: reviewer\nkind: review\nprompt: roles/reviewer.md\nmode: blocking\nagent: "+agent+"\nmodel: "+model+"\n")
}

func TestValidateRoles(t *testing.T) {
	specs.Describe(t, "ValidateRoles", func(s *specs.Spec) {
		s.It("warns only about the repo's security reviewer, on the builder's agent and model", func(ctx *specs.Context) {
			warnings, err := wiring.ValidateRoles(repoRoot)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(warnings).ToEqual([]string{"reviewer-security uses the same agent and model as builder: claude opus"})
		})

		s.It("has no warnings when the reviewer runs on another model", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			setReviewer(ctx, dir, "claude", "sonnet")

			warnings, err := wiring.ValidateRoles(dir)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(warnings).To(specs.BeEmpty())
		})

		s.It("does not warn about a role of another kind on the builder's agent and model", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			write(ctx, dir, "roles/approver.md", "Approve.\n")
			write(ctx, dir, "roles/approver.yaml", "name: approver\nkind: approve\nprompt: roles/approver.md\nmode: blocking\nagent: claude\nmodel: opus\n")

			warnings, err := wiring.ValidateRoles(dir)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(warnings).To(specs.BeEmpty())
		})

		s.It("fails with the role's load error when a role names an agent that is not configured", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			setReviewer(ctx, dir, "nadie", "opus")
			_, loadErr := wiring.LoadRoleSession(dir, "reviewer")

			_, err := wiring.ValidateRoles(dir)

			ctx.Expect(err).To(specs.MatchError(configyaml.ErrUnknownAgent))
			ctx.Expect(err.Error()).ToEqual(loadErr.Error())
		})

		s.It("fails on a root with no role files, naming the root", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()

			_, err := wiring.ValidateRoles(dir)

			ctx.Expect(err).To(specs.MatchError(wiring.ErrNoRoles))
			ctx.Expect(err.Error()).To(specs.Contain("no roles/*.yaml in " + dir))
		})

		s.It("fails naming the pipeline and the role when a pipeline names a role with no file", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			setReviewer(ctx, dir, "claude", "sonnet")
			write(ctx, dir, "pipelines/flow.yaml", "name: flow\nstages:\n  - build\n  - review:\n      parallel: [reviewer, ghost]\n      on_changes: build\n  - approve\n")

			_, err := wiring.ValidateRoles(dir)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(`pipelines/flow.yaml: stage review: invalid value: role "ghost" is not loaded`)
		})

		s.It("fails on a role file named for another role", func(ctx *specs.Context) {
			dir := configRoot(ctx, "claude", "opus")
			write(ctx, dir, "roles/approver.yaml", "name: builder\nkind: build\nprompt: roles/builder.md\nmode: blocking\nagent: claude\nmodel: opus\n")

			_, err := wiring.ValidateRoles(dir)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}

func TestRepoPipelines(t *testing.T) {
	specs.Describe(t, "the repo's pipelines", func(s *specs.Spec) {
		s.It("each load with the repo's roles", func(ctx *specs.Context) {
			roleFiles, err := filepath.Glob(filepath.Join(repoRoot, "roles", "*.yaml"))
			ctx.Expect(err).To(specs.BeNil())
			var roles []domain.Role
			for _, f := range roleFiles {
				rs, err := wiring.LoadRoleSession(repoRoot, strings.TrimSuffix(filepath.Base(f), ".yaml"))
				ctx.Expect(err).To(specs.BeNil())
				roles = append(roles, rs.Role)
			}
			files, err := filepath.Glob(filepath.Join(repoRoot, "pipelines", "*.yaml"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(files).To(specs.Not(specs.BeEmpty()))
			for _, f := range files {
				data, err := os.ReadFile(f)
				ctx.Expect(err).To(specs.BeNil())

				_, err = configyaml.LoadPipeline(data, roles)

				ctx.Expect(err).To(specs.BeNil())
			}
		})
	})
}
