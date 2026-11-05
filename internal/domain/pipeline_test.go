package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func validPipeline() domain.Pipeline {
	return domain.Pipeline{
		Name: "default",
		Stages: []domain.Stage{
			{Name: "intake", Kind: domain.KindIntake},
			{Name: "build", Kind: domain.KindBuild},
			{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build", MaxRounds: 3},
			{Name: "approve", Kind: domain.KindApprove},
		},
	}
}

type pipelineRow struct {
	name     string
	edit     func(*domain.Pipeline)
	sentinel error
	want     string
}

func TestPipeline(t *testing.T) {
	roles := []domain.Role{{Name: "reviewer-code", Kind: domain.KindReview, Prompt: "roles/reviewer-code.md"}}

	specs.Describe(t, "Pipeline.Validate", func(s *specs.Spec) {
		s.It("accepts one build and a review that goes back to it", func(ctx *specs.Context) {
			ctx.Expect(validPipeline().Validate(roles)).To(specs.BeNil())
		})

		s.When("a flow rule is broken", func(s *specs.Spec) {
			rows := []pipelineRow{
				{"rejects no build stage", func(p *domain.Pipeline) {
					p.Stages = []domain.Stage{p.Stages[0], p.Stages[3]}
				}, domain.ErrInvalidValue, "pipeline default: invalid value: 0 build stages, want exactly 1"},
				{"rejects two build stages", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "build2", Kind: domain.KindBuild})
				}, domain.ErrInvalidValue, "pipeline default: invalid value: 2 build stages, want exactly 1"},
				{"names a review without on_changes", func(p *domain.Pipeline) {
					p.Stages[2].OnChanges = ""
				}, domain.ErrMissingField, "stage review: missing field: on_changes"},
				{"rejects on_changes to a later stage", func(p *domain.Pipeline) {
					p.Stages[2].OnChanges = "approve"
				}, domain.ErrInvalidValue, `stage review: invalid value: on_changes "approve" is not an earlier stage`},
				{"names the stage and the unknown role", func(p *domain.Pipeline) {
					p.Stages[2].Roles = []string{"reviewer-code", "reviewer-perf"}
				}, domain.ErrInvalidValue, `stage review: invalid value: role "reviewer-perf" is not loaded`},
				{"rejects an unknown kind", func(p *domain.Pipeline) {
					p.Stages[0] = domain.Stage{Name: "deploy", Kind: "deploy"}
				}, domain.ErrInvalidValue, `stage deploy: invalid value: kind "deploy"`},
			}
			specs.Table(s, rows, func(r pipelineRow) string {
				return r.name
			}, func(ctx *specs.Context, r pipelineRow) {
				p := validPipeline()
				r.edit(&p)

				err := p.Validate(roles)

				ctx.Expect(err).To(specs.MatchError(r.sentinel))
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})
	})
}
