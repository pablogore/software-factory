package configyaml_test

import (
	"os"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

type stageResultRow struct {
	name   string
	stage  string
	role   string
	result domain.Result
}

func TestLoadPipelineDefaultRoles(t *testing.T) {
	specs.Describe(t, "LoadPipeline with stages that list no roles", func(s *specs.Spec) {
		s.It("gives a roleless build the builder role without a role file", func(ctx *specs.Context) {
			p, err := configyaml.LoadPipeline([]byte("name: p\nstages: [build]\n"), nil)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p.Stages[0].Roles).ToEqual([]string{"builder"})
			ctx.Expect(p.Roles).To(specs.BeEmpty())
		})

		s.It("keeps a default role's file when it is loaded", func(ctx *specs.Context) {
			approver, err := configyaml.LoadRole([]byte("name: approver\nkind: approve\nprompt: roles/approver.md\nmode: blocking\n"))
			ctx.Expect(err).To(specs.BeNil())
			roles := append(loadedRoles(ctx), approver)
			data, err := os.ReadFile("../../../../pipelines/default.yaml")
			ctx.Expect(err).To(specs.BeNil())

			p, err := configyaml.LoadPipeline(data, roles)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p.Roles).ToEqual(roles)
		})

		s.When("pipelines/default.yaml gets a result from a role outside the current stage", func(s *specs.Spec) {
			rows := []stageResultRow{
				{"refuses the builder answering approve", "approve", "builder", domain.ApproveResult{Decision: domain.ApproveApprove}},
				{"refuses the approver answering build", "build", "approver", domain.BuildResult{Status: domain.BuildReady}},
				{"refuses the builder answering intake", "intake", "builder", domain.IntakeResult{Decision: domain.IntakeAccept}},
				{"refuses the intake role answering spec", "spec", "intake", domain.SpecResult{Spec: "# S1"}},
			}
			specs.Table(s, rows, func(r stageResultRow) string { return r.name }, func(ctx *specs.Context, r stageResultRow) {
				data, err := os.ReadFile("../../../../pipelines/default.yaml")
				ctx.Expect(err).To(specs.BeNil())
				p, err := configyaml.LoadPipeline(data, loadedRoles(ctx))
				ctx.Expect(err).To(specs.BeNil())
				job := domain.Job{ID: "job-roles", Pipeline: p.Name, Stage: r.stage, Round: 1}
				before := job
				ev := domain.ResultSubmitted{Job: job.ID, Role: r.role, Result: r.result, At: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)}

				evs, err := decide.Decide(job, ev, p)

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.It("lets the approver answer approve in pipelines/default.yaml", func(ctx *specs.Context) {
			data, err := os.ReadFile("../../../../pipelines/default.yaml")
			ctx.Expect(err).To(specs.BeNil())
			p, err := configyaml.LoadPipeline(data, loadedRoles(ctx))
			ctx.Expect(err).To(specs.BeNil())
			job := domain.Job{ID: "job-roles", Pipeline: p.Name, Stage: "approve", Round: 1}
			ev := domain.ResultSubmitted{Job: job.ID, Role: "approver", Result: domain.ApproveResult{Decision: domain.ApproveApprove}, At: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)}

			_, err = decide.Decide(job, ev, p)

			ctx.Expect(err).To(specs.BeNil())
		})
	})
}
