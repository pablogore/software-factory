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

func ptr(n int) *int { return &n }

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

		s.It("accepts every limit set to 1", func(ctx *specs.Context) {
			p := validPipeline()
			p.Limits = domain.Limits{SpecRejections: ptr(1), HumanReworks: ptr(1), TriageRetries: ptr(1), MaxRounds: ptr(1)}
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.When("a flow rule is broken", func(s *specs.Spec) {
			rows := []pipelineRow{
				{"rejects no build stage", func(p *domain.Pipeline) {
					p.Stages = []domain.Stage{p.Stages[0], p.Stages[3]}
				}, domain.ErrInvalidValue, "pipeline default: invalid value: 0 build stages, want exactly 1"},
				{"rejects two build stages", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "build2", Kind: domain.KindBuild})
				}, domain.ErrInvalidValue, "pipeline default: invalid value: 2 build stages, want exactly 1"},
				{"leaves no blank for a pipeline without a name", func(p *domain.Pipeline) {
					p.Name = ""
					p.Stages = []domain.Stage{p.Stages[0], p.Stages[3]}
				}, domain.ErrInvalidValue, "pipeline: invalid value: 0 build stages, want exactly 1"},
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
				{"names a review with max_rounds 0", func(p *domain.Pipeline) {
					p.Stages[2].MaxRounds = 0
				}, domain.ErrInvalidValue, "stage review: invalid value: max_rounds 0, want at least 1"},
				{"names a review with negative max_rounds", func(p *domain.Pipeline) {
					p.Stages[2].MaxRounds = -1
				}, domain.ErrInvalidValue, "stage review: invalid value: max_rounds -1, want at least 1"},
				{"names the repeated stage", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "intake", Kind: domain.KindIntake})
				}, domain.ErrInvalidValue, "stage intake: invalid value: stage name is repeated"},
				{"names a review that lists no roles", func(p *domain.Pipeline) {
					p.Stages[2].Roles = nil
				}, domain.ErrMissingField, "stage review: missing field: parallel"},
				{"names a stopped stage of kind intake", func(p *domain.Pipeline) {
					p.Stages[0].Name = "stopped"
				}, domain.ErrInvalidValue, "stage stopped: invalid value: stage name is reserved"},
				{"names a merge stage of kind approve", func(p *domain.Pipeline) {
					p.Stages[3].Name = "merge"
				}, domain.ErrInvalidValue, "stage merge: invalid value: stage name is reserved"},
				{"names a needs-human stage of kind review", func(p *domain.Pipeline) {
					p.Stages[2].Name = "needs-human"
				}, domain.ErrInvalidValue, "stage needs-human: invalid value: stage name is reserved"},
				{"names a needs-info stage of kind task", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "needs-info", Kind: domain.KindTask})
				}, domain.ErrInvalidValue, "stage needs-info: invalid value: stage name is reserved"},
				{"names a done stage of kind build", func(p *domain.Pipeline) {
					p.Stages[1].Name = "done"
					p.Stages[2].OnChanges = "done"
				}, domain.ErrInvalidValue, "stage done: invalid value: stage name is reserved"},
				{"names a rejected stage of kind spec", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "rejected", Kind: domain.KindSpec})
				}, domain.ErrInvalidValue, "stage rejected: invalid value: stage name is reserved"},
				{"names spec_rejections 0", func(p *domain.Pipeline) {
					p.Limits.SpecRejections = ptr(0)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: spec_rejections 0, want at least 1"},
				{"names negative human_reworks", func(p *domain.Pipeline) {
					p.Limits.HumanReworks = ptr(-1)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: human_reworks -1, want at least 1"},
				{"names triage_retries 0", func(p *domain.Pipeline) {
					p.Limits.TriageRetries = ptr(0)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: triage_retries 0, want at least 1"},
				{"names max_rounds 0 in limits", func(p *domain.Pipeline) {
					p.Limits.MaxRounds = ptr(0)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: max_rounds 0, want at least 1"},
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

func TestPipelineSpecRejections(t *testing.T) {
	specs.Describe(t, "Pipeline.SpecRejections", func(s *specs.Spec) {
		s.It("is 3 when limits does not set it", func(ctx *specs.Context) {
			ctx.Expect(validPipeline().SpecRejections()).ToEqual(3)
		})

		s.It("is limits.spec_rejections when set", func(ctx *specs.Context) {
			p := validPipeline()
			p.Limits.SpecRejections = ptr(5)
			ctx.Expect(p.SpecRejections()).ToEqual(5)
		})
	})
}
