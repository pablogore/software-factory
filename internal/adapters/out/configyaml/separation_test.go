package configyaml_test

import (
	"os"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

type separationRow struct {
	name  string
	yaml  string
	extra []string
	want  string
}

// roleFile loads one role file given as YAML, as an operator would declare it.
func roleFile(ctx *specs.Context, data string) domain.Role {
	r, err := configyaml.LoadRole([]byte(data))
	ctx.Expect(err).To(specs.BeNil())
	return r
}

const (
	builderFile       = "name: builder\nkind: build\nprompt: roles/builder.md\nmode: blocking\n"
	intakeBuilderFile = "name: builder\nkind: intake\nprompt: roles/builder.md\nmode: blocking\n"
	otherCodeFile     = "name: reviewer-code\nkind: review\nprompt: roles/other-code.md\nmode: advisory\n"
)

func TestLoadPipelineSeparationOfDuties(t *testing.T) {
	specs.Describe(t, "LoadPipeline with roles in the wrong stage", func(s *specs.Spec) {
		s.It("loads pipelines/default.yaml with a builder role file of kind build", func(ctx *specs.Context) {
			data, err := os.ReadFile("../../../../pipelines/default.yaml")
			ctx.Expect(err).To(specs.BeNil())
			roles := append(loadedRoles(ctx), roleFile(ctx, builderFile))

			p, err := configyaml.LoadPipeline(data, roles)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p.Stages[2].Roles).ToEqual([]string{"builder"})
		})

		rows := []separationRow{
			{
				"names the approve stage listing the builder",
				"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve:\n      parallel: [builder]\n",
				[]string{builderFile},
				`stage approve: invalid value: role "builder" has kind build, want approve`,
			},
			{
				"names the approve stage listing a reviewer",
				"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve:\n      parallel: [reviewer-ux]\n",
				nil,
				`stage approve: invalid value: role "reviewer-ux" has kind review, want approve`,
			},
			{
				"names the review stage listing the builder",
				"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code, builder]\n      on_changes: build\n  - approve\n",
				[]string{builderFile},
				`stage review: invalid value: role "builder" has kind build, want review`,
			},
			{
				"names the intake stage listing the builder while it fills build by default",
				"name: p\nstages:\n  - intake:\n      parallel: [builder]\n" + shortFlow[len("name: p\nstages:\n"):],
				[]string{builderFile},
				`stage intake: invalid value: role "builder" has kind build, want intake`,
			},
			{
				"names the build stage the builder fills by default once it is in intake",
				"name: p\nstages:\n  - intake:\n      parallel: [builder]\n" + shortFlow[len("name: p\nstages:\n"):],
				[]string{intakeBuilderFile},
				`stage build: invalid value: role "builder" has kind intake, want build`,
			},
			{
				"names a role loaded from two reviewer-code role files",
				shortFlow,
				[]string{otherCodeFile},
				`pipeline p: invalid value: role "reviewer-code" is loaded twice`,
			},
		}
		specs.Table(s, rows, func(r separationRow) string { return r.name }, func(ctx *specs.Context, r separationRow) {
			roles := loadedRoles(ctx)
			for _, data := range r.extra {
				roles = append(roles, roleFile(ctx, data))
			}

			_, err := configyaml.LoadPipeline([]byte(r.yaml), roles)

			ctx.Testing().Log(err)
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(r.want)
		})
	})
}
