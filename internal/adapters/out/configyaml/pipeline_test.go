package configyaml_test

import (
	"os"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

var reviewers = []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "reviewer-code", "security-deep"}

// shortFlow is the smallest stage list that loads: build, review, one approve.
const shortFlow = "name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\n"

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

			roles := loadedRoles(ctx)
			p, err := configyaml.LoadPipeline(data, roles)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p).ToEqual(domain.Pipeline{
				Name: "default",
				Stages: []domain.Stage{
					{Name: "intake", Kind: domain.KindIntake, Roles: []string{"intake"}},
					{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
					{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
					{Name: "review", Kind: domain.KindReview, Roles: reviewers, OnChanges: "build", MaxRounds: new(3)},
					{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
				},
				Roles: roles,
			})
		})

		s.It("loads a limits block", func(ctx *specs.Context) {
			data := shortFlow + "limits:\n  spec_rejections: 1\n  human_reworks: 2\n  triage_retries: 1\n  max_rounds: 4\n"

			p, err := configyaml.LoadPipeline([]byte(data), loadedRoles(ctx))

			ctx.Expect(err).To(specs.BeNil())
			one, two, four := 1, 2, 4
			ctx.Expect(p.Limits).ToEqual(domain.Limits{SpecRejections: &one, HumanReworks: &two, TriageRetries: &one, MaxRounds: &four})
			ctx.Expect(p.SpecRejections()).ToEqual(1)
		})

		s.It("loads every limit at its cap", func(ctx *specs.Context) {
			data := shortFlow + "limits:\n  spec_rejections: 10\n  human_reworks: 10\n  triage_retries: 3\n  max_rounds: 10\n"

			p, err := configyaml.LoadPipeline([]byte(data), loadedRoles(ctx))

			ctx.Expect(err).To(specs.BeNil())
			ten, three := 10, 3
			ctx.Expect(p.Limits).ToEqual(domain.Limits{SpecRejections: &ten, HumanReworks: &ten, TriageRetries: &three, MaxRounds: &ten})
		})

		s.When("a copy of pipelines/default.yaml sets the review stage's max_rounds", func(s *specs.Spec) {
			// The copy is edited in memory: pipelines/default.yaml itself stays as it is.
			withRounds := func(ctx *specs.Context, rounds string) []byte {
				data, err := os.ReadFile("../../../../pipelines/default.yaml")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(strings.Count(string(data), "max_rounds: 3\n")).ToEqual(1)
				return []byte(strings.Replace(string(data), "max_rounds: 3\n", "max_rounds: "+rounds+"\n", 1))
			}

			s.It("names max_rounds and its cap of 10 at 11", func(ctx *specs.Context) {
				_, err := configyaml.LoadPipeline(withRounds(ctx, "11"), loadedRoles(ctx))

				ctx.Testing().Log(err)
				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
				ctx.Expect(err.Error()).ToEqual("stage review: invalid value: max_rounds 11, want at most 10")
			})

			s.It("loads it at 10", func(ctx *specs.Context) {
				p, err := configyaml.LoadPipeline(withRounds(ctx, "10"), loadedRoles(ctx))

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(p.MaxRounds(p.Stages[3])).ToEqual(10)
			})
		})

		s.It("loads a task stage after the approve", func(ctx *specs.Context) {
			scribe, err := configyaml.LoadRole([]byte("name: scribe\nkind: task\nprompt: roles/scribe.md\nmode: blocking\n"))
			ctx.Expect(err).To(specs.BeNil())

			p, err := configyaml.LoadPipeline([]byte(shortFlow+"  - task:\n      parallel: [scribe]\n"), append(loadedRoles(ctx), scribe))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p.Stages[3]).ToEqual(domain.Stage{Name: "task", Kind: domain.KindTask, Roles: []string{"scribe"}})
		})

		s.It("loads human_reworks unlimited as no limit", func(ctx *specs.Context) {
			p, err := configyaml.LoadPipeline([]byte(shortFlow+"limits:\n  human_reworks: unlimited\n"), loadedRoles(ctx))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p.Limits).ToEqual(domain.Limits{})
		})

		s.It("takes 3 spec rejections with an empty limits block", func(ctx *specs.Context) {
			p, err := configyaml.LoadPipeline([]byte(shortFlow+"limits:\n"), loadedRoles(ctx))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p.SpecRejections()).ToEqual(3)
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
					"names the stage and the role listed twice",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code, reviewer-ux, reviewer-code]\n      on_changes: build\n  - approve\n",
					`stage review: invalid value: role "reviewer-code" is listed twice`,
				},
				{
					"names the stage and a role with an uppercase letter",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [Reviewer-code]\n      on_changes: build\n  - approve\n",
					`stage review: invalid value: role name "Reviewer-code", want only lowercase letters, digits and hyphens`,
				},
				{
					"names the stage and a role with an underscore",
					"name: p\nstages:\n  - build:\n      parallel: [builder_x]\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\n",
					`stage build: invalid value: role name "builder_x", want only lowercase letters, digits and hyphens`,
				},
				{
					"names the stage and a role that would repeat one but for a trailing space",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code, \"reviewer-code \"]\n      on_changes: build\n  - approve\n",
					`stage review: invalid value: role name "reviewer-code ", want only lowercase letters, digits and hyphens`,
				},
				{
					"names the stage and a role with a Cyrillic look-alike",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-cod\u0435]\n      on_changes: build\n  - approve\n",
					`stage review: invalid value: role name "reviewer-cod\u0435", want only lowercase letters, digits and hyphens`,
				},
				{
					"names the stage and a role starting with a hyphen",
					"name: p\nstages:\n  - build:\n      parallel: [-builder]\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\n",
					`stage build: invalid value: role name "-builder" starts or ends with a hyphen`,
				},
				{
					"names the stage and an empty role",
					"name: p\nstages:\n  - build:\n      parallel: [\"\"]\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\n",
					"stage build: invalid value: role name is empty",
				},
				{
					"names the stage and a role over 64 characters",
					"name: p\nstages:\n  - build:\n      parallel: [" + strings.Repeat("b", 65) + "]\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\n",
					`stage build: invalid value: role name "` + strings.Repeat("b", 65) + `" is 65 characters, want at most 64`,
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
					`parse pipeline: invalid value: line 2: unknown key "stage"`,
				},
				{
					"rejects an empty pipeline file",
					"",
					"parse pipeline: invalid value: file is empty",
				},
				{
					"leaves no blank for a stage error in a pipeline without a name",
					"stages:\n  - build\n  - {review: {on_changes: build}, approve: {}}\n",
					"pipeline: stages[1]: invalid value: line 3: want a stage name or a one-key map",
				},
				{
					"leaves no blank for a pipeline without a name and no build stage",
					"stages: [intake, approve]\n",
					"pipeline: invalid value: 0 build stages, want exactly 1",
				},
				{
					"names the review stage with max_rounds 0",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n      max_rounds: 0\n",
					"stage review: invalid value: max_rounds 0, want at least 1",
				},
				{
					"names the review stage with negative max_rounds",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n      max_rounds: -2\n",
					"stage review: invalid value: max_rounds -2, want at least 1",
				},
				{
					"names the repeated stage",
					"name: p\nstages: [intake, intake, build]\n",
					"stage intake: invalid value: stage name is repeated",
				},
				{
					"names the review stage that lists no roles",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: []\n      on_changes: build\n      max_rounds: 3\n",
					"stage review: missing field: parallel",
				},
				{
					"names the review stage with no parallel key",
					"name: p\nstages:\n  - build\n  - review:\n      on_changes: build\n      max_rounds: 3\n",
					"stage review: missing field: parallel",
				},
				{
					"names an unknown key in limits",
					"name: p\nstages: [build]\nlimits:\n  spec_rejectionz: 2\n",
					`pipeline p: limits: invalid value: line 4: unknown key "spec_rejectionz"`,
				},
				{
					"names spec_rejections 0",
					"name: p\nstages: [build]\nlimits:\n  spec_rejections: 0\n",
					"pipeline p: limits: invalid value: spec_rejections 0, want at least 1",
				},
				{
					"names negative max_rounds in limits",
					"name: p\nstages: [build]\nlimits:\n  max_rounds: -1\n",
					"pipeline p: limits: invalid value: max_rounds -1, want at least 1",
				},
				{
					"names a spec_rejections that is not a whole number",
					"name: p\nstages: [build]\nlimits:\n  spec_rejections: 2.5\n",
					`pipeline p: limits: invalid value: line 4: spec_rejections "2.5", want a whole number of 1 or more`,
				},
				{
					"names a triage_retries that is text",
					"name: p\nstages: [build]\nlimits:\n  triage_retries: two\n",
					`pipeline p: limits: invalid value: line 4: triage_retries "two", want a whole number of 1 or more`,
				},
				{
					"names a human_reworks that is neither a number nor unlimited",
					"name: p\nstages: [build]\nlimits:\n  human_reworks: forever\n",
					`pipeline p: limits: invalid value: line 4: human_reworks "forever", want a whole number of 1 or more or unlimited`,
				},
				{
					"names human_reworks 0",
					"name: p\nstages: [build]\nlimits:\n  human_reworks: 0\n",
					"pipeline p: limits: invalid value: human_reworks 0, want at least 1",
				},
				{
					"names spec_rejections above its cap of 10",
					"name: p\nstages: [build]\nlimits:\n  spec_rejections: 11\n",
					"pipeline p: limits: invalid value: spec_rejections 11, want at most 10",
				},
				{
					"names human_reworks above its cap of 10",
					"name: p\nstages: [build]\nlimits:\n  human_reworks: 11\n",
					"pipeline p: limits: invalid value: human_reworks 11, want at most 10",
				},
				{
					"names triage_retries above its cap of 3",
					"name: p\nstages: [build]\nlimits:\n  triage_retries: 4\n",
					"pipeline p: limits: invalid value: triage_retries 4, want at most 3",
				},
				{
					"names max_rounds in limits above its cap of 10",
					"name: p\nstages: [build]\nlimits:\n  max_rounds: 11\n",
					"pipeline p: limits: invalid value: max_rounds 11, want at most 10",
				},
				{
					"names the review stage with max_rounds above its cap of 10",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n      max_rounds: 11\n",
					"stage review: invalid value: max_rounds 11, want at most 10",
				},
				{
					"rejects limits that are not a map",
					"name: p\nstages: [build]\nlimits: 3\n",
					"pipeline p: limits: invalid value: line 3: want a map of limits",
				},
				{
					"names a task stage that lists no roles, a kind with no default",
					shortFlow + "  - task\n",
					"stage task: missing field: kind task lists no roles and has no default role",
				},
				{
					"names an approve stage with no review stage",
					"name: p\nstages: [intake, build, approve]\n",
					"stage approve: invalid value: approve stage has no review stage before it",
				},
				{
					"names an approve stage that comes before review",
					"name: p\nstages:\n  - build\n  - approve\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n",
					"stage approve: invalid value: approve stage has no review stage before it",
				},
				{
					"names a pipeline with no approve stage",
					"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n",
					"pipeline p: missing field: approve stage",
				},
				{
					// A YAML stage's kind is its name, so a second approve is first a repeated name.
					"names a second approve stage",
					shortFlow + "  - approve\n",
					"stage approve: invalid value: stage name is repeated",
				},
				{
					"names a review stage before the build",
					"name: p\nstages:\n  - intake\n  - review:\n      parallel: [reviewer-code]\n      on_changes: intake\n  - build\n  - approve\n",
					"stage review: invalid value: review stage comes before the build stage",
				},
				{
					"names a spec stage after the approve",
					shortFlow + "  - spec\n",
					"stage spec: invalid value: only task stages may come after the approve stage",
				},
				{
					"names a bare stage called merge",
					"name: p\nstages: [intake, build, merge]\n",
					"stage merge: invalid value: stage name is reserved",
				},
				{
					"names a map stage called done",
					"name: p\nstages:\n  - build\n  - done:\n      parallel: [reviewer-code]\n      on_changes: build\n      max_rounds: 3\n",
					"stage done: invalid value: stage name is reserved",
				},
			}
			specs.Table(s, rows, func(r brokenRow) string {
				return r.name
			}, func(ctx *specs.Context, r brokenRow) {
				_, err := configyaml.LoadPipeline([]byte(r.yaml), loadedRoles(ctx))

				// Printed so `go test -v` shows the operator's message.
				ctx.Testing().Log(err)
				ctx.Expect(err == nil).ToEqual(false)
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})
	})
}
