package configyaml_test

import (
	"os"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

var reviewers = []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "reviewer-code", "security-deep"}

// loadedRoles loads the role files the default pipeline lists, so pipelines
// are checked against roles as an operator declares them.
func loadedRoles(ctx *specs.Context) []domain.Role {
	roles := make([]domain.Role, 0, len(reviewers))
	for _, name := range reviewers {
		data, err := os.ReadFile("testdata/roles/" + name + ".yaml")
		ctx.Expect(err).To(specs.BeNil())
		r, err := configyaml.LoadRole(data)
		ctx.Expect(err).To(specs.BeNil())
		roles = append(roles, r)
	}
	return roles
}

type brokenRow struct {
	name string
	yaml string
	want string
}

func TestLoadPipeline(t *testing.T) {
	specs.Describe(t, "LoadPipeline", func(s *specs.Spec) {
		s.It("loads pipelines/default.yaml as the 04-roles.md flow", func(ctx *specs.Context) {
			data, err := os.ReadFile("../../../../pipelines/default.yaml")
			ctx.Expect(err).To(specs.BeNil())

			p, err := configyaml.LoadPipeline(data, loadedRoles(ctx))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p).ToEqual(domain.Pipeline{
				Name: "default",
				Stages: []domain.Stage{
					{Name: "intake", Kind: domain.KindIntake},
					{Name: "spec", Kind: domain.KindSpec},
					{Name: "build", Kind: domain.KindBuild},
					{Name: "review", Kind: domain.KindReview, Roles: reviewers, OnChanges: "build", MaxRounds: 3},
					{Name: "approve", Kind: domain.KindApprove},
				},
			})
		})

		s.When("the flow is broken", func(s *specs.Spec) {
			rows := []brokenRow{
				{
					"rejects a pipeline with no build stage",
					"name: p\nstages: [intake, spec, approve]\n",
					"pipeline p: invalid value: 0 build stages, want exactly 1",
				},
				{
					"rejects a pipeline with two build stages",
					"name: p\nstages: [intake, build, build, approve]\n",
					"pipeline p: invalid value: 2 build stages, want exactly 1",
				},
				{
					"names the review stage without on_changes",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n",
					"stage review: missing field: on_changes",
				},
				{
					"names the stage and the role that is not loaded",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code, reviewer-perf]\n      on_changes: build\n",
					`stage review: invalid value: role "reviewer-perf" is not loaded`,
				},
				{
					"rejects a stage that is neither a name nor a one-key map",
					"name: p\nstages:\n  - build\n  - {review: {on_changes: build}, approve: {}}\n",
					"pipeline p: stages[1]: invalid value: line 4: want a stage name or a one-key map",
				},
				{
					"names an unknown key in a stage",
					"name: p\nstages:\n  - build\n  - review:\n      paralel: [reviewer-code]\n      on_changes: build\n", //nolint:misspell // the typo under test
					`pipeline p: stages[1]: stage review: invalid value: line 5: unknown key "paralel"`,                   //nolint:misspell // the typo under test
				},
				{
					"names an unknown top-level key",
					"name: p\nstage: [build]\n",
					"parse pipeline: yaml: unmarshal errors:\n  line 2: field stage not found in type configyaml.pipelineFile",
				},
			}
			specs.Table(s, rows, func(r brokenRow) string {
				return r.name
			}, func(ctx *specs.Context, r brokenRow) {
				_, err := configyaml.LoadPipeline([]byte(r.yaml), loadedRoles(ctx))

				ctx.Expect(err == nil).ToEqual(false)
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})
	})
}
