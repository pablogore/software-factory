package domain_test

import (
	"slices"
	"strings"
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
			{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build", MaxRounds: ptr(3)},
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
	roles := []domain.Role{
		{Name: "reviewer-code", Kind: domain.KindReview, Prompt: "roles/reviewer-code.md"},
		{Name: "reviewer-ux", Kind: domain.KindReview, Prompt: "roles/reviewer-ux.md"},
		{Name: "scribe", Kind: domain.KindTask, Prompt: "roles/scribe.md"},
		{Name: "notifier", Kind: domain.KindTask, Prompt: "roles/notifier.md"},
		{Name: "builder", Kind: domain.KindBuild, Prompt: "roles/builder.md"},
	}

	specs.Describe(t, "Pipeline.Validate", func(s *specs.Spec) {
		s.It("accepts one build and a review that goes back to it", func(ctx *specs.Context) {
			ctx.Expect(validPipeline().Validate(roles)).To(specs.BeNil())
		})

		s.It("accepts every limit set to 1", func(ctx *specs.Context) {
			p := validPipeline()
			p.Limits = domain.Limits{SpecRejections: ptr(1), HumanReworks: ptr(1), TriageRetries: ptr(1), MaxRounds: ptr(1)}
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.It("accepts every limit at its cap and a review at the max_rounds cap", func(ctx *specs.Context) {
			p := validPipeline()
			p.Limits = domain.Limits{SpecRejections: ptr(10), HumanReworks: ptr(10), TriageRetries: ptr(3), MaxRounds: ptr(10)}
			p.Stages[2].MaxRounds = ptr(10)
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.It("accepts task stages after the approve", func(ctx *specs.Context) {
			p := validPipeline()
			p.Stages = append(p.Stages,
				domain.Stage{Name: "changelog", Kind: domain.KindTask, Roles: []string{"scribe"}},
				domain.Stage{Name: "notify", Kind: domain.KindTask, Roles: []string{"notifier"}},
			)
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.It("accepts a review that leaves out max_rounds", func(ctx *specs.Context) {
			p := validPipeline()
			p.Stages[2].MaxRounds = nil
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.It("accepts several reviewers in one review stage", func(ctx *specs.Context) {
			p := validPipeline()
			p.Stages[2].Roles = []string{"reviewer-code", "reviewer-ux"}
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.It("accepts two review stages with different reviewers", func(ctx *specs.Context) {
			p := validPipeline()
			p.Stages = []domain.Stage{
				p.Stages[0], p.Stages[1], p.Stages[2],
				{Name: "ux-review", Kind: domain.KindReview, Roles: []string{"reviewer-ux"}, OnChanges: "build"},
				p.Stages[3],
			}
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.It("accepts the builder file in build, listed or by default", func(ctx *specs.Context) {
			p := validPipeline()
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
			p.Stages[1].Roles = []string{"builder"}
			ctx.Expect(p.Validate(roles)).To(specs.BeNil())
		})

		s.It("names a roleless stage whose default role file has another kind", func(ctx *specs.Context) {
			approver := domain.Role{Name: "approver", Kind: domain.KindReview, Prompt: "roles/approver.md"}

			err := validPipeline().Validate(append(slices.Clone(roles), approver))

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(`stage approve: invalid value: role "approver" has kind review, want approve`)
		})

		s.It("names a stage listing a role another stage takes by default", func(ctx *specs.Context) {
			intake := domain.Role{Name: "intake", Kind: domain.KindIntake, Prompt: "roles/intake.md"}
			p := validPipeline()
			p.Stages = append([]domain.Stage{{Name: "triage", Kind: domain.KindIntake}}, p.Stages...)
			p.Stages[1].Roles = []string{"intake"}

			err := p.Validate(append(slices.Clone(roles), intake))

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(`stage intake: invalid value: role "intake" is also in stage triage`)
		})

		s.It("names a role loaded from two role files", func(ctx *specs.Context) {
			again := domain.Role{Name: "reviewer-code", Kind: domain.KindReview, Prompt: "roles/other.md"}

			err := validPipeline().Validate(append(slices.Clone(roles), again))

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(`pipeline default: invalid value: role "reviewer-code" is loaded twice`)
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
				{"names the stage and the role listed twice", func(p *domain.Pipeline) {
					p.Stages[2].Roles = []string{"reviewer-code", "reviewer-ux", "reviewer-code"}
				}, domain.ErrInvalidValue, `stage review: invalid value: role "reviewer-code" is listed twice`},
				{"names the stage and an uppercase role", func(p *domain.Pipeline) {
					p.Stages[2].Roles = []string{"Reviewer-code"}
				}, domain.ErrInvalidValue, `stage review: invalid value: role name "Reviewer-code", want only lowercase letters, digits and hyphens`},
				{"names a look-alike role before saying it is not loaded", func(p *domain.Pipeline) {
					p.Stages[2].Roles = []string{"reviewer-cod\u0435"}
				}, domain.ErrInvalidValue, `stage review: invalid value: role name "reviewer-cod\u0435", want only lowercase letters, digits and hyphens`},
				{"names a role with a trailing space that would repeat one listed", func(p *domain.Pipeline) {
					p.Stages[2].Roles = []string{"reviewer-code", "reviewer-code "}
				}, domain.ErrInvalidValue, `stage review: invalid value: role name "reviewer-code ", want only lowercase letters, digits and hyphens`},
				{"names the stage and an empty role", func(p *domain.Pipeline) {
					p.Stages[1].Roles = []string{""}
				}, domain.ErrInvalidValue, "stage build: invalid value: role name is empty"},
				{"names the stage and a role starting with a hyphen", func(p *domain.Pipeline) {
					p.Stages[1].Roles = []string{"-builder"}
				}, domain.ErrInvalidValue, `stage build: invalid value: role name "-builder" starts or ends with a hyphen`},
				{"names the stage and a role over 64 characters", func(p *domain.Pipeline) {
					p.Stages[1].Roles = []string{strings.Repeat("b", 65)}
				}, domain.ErrInvalidValue, `stage build: invalid value: role name "` + strings.Repeat("b", 65) + `" is 65 characters, want at most 64`},
				{"rejects an unknown kind", func(p *domain.Pipeline) {
					p.Stages[0] = domain.Stage{Name: "deploy", Kind: "deploy"}
				}, domain.ErrInvalidValue, `stage deploy: invalid value: kind "deploy"`},
				{"names a review with max_rounds 0", func(p *domain.Pipeline) {
					p.Stages[2].MaxRounds = ptr(0)
				}, domain.ErrInvalidValue, "stage review: invalid value: max_rounds 0, want at least 1"},
				{"names a review with negative max_rounds", func(p *domain.Pipeline) {
					p.Stages[2].MaxRounds = ptr(-1)
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
				{"names a needs-human stage of kind approve", func(p *domain.Pipeline) {
					p.Stages[3].Name = "needs-human"
				}, domain.ErrInvalidValue, "stage needs-human: invalid value: stage name is reserved"},
				{"names a done stage of kind approve", func(p *domain.Pipeline) {
					p.Stages[3].Name = "done"
				}, domain.ErrInvalidValue, "stage done: invalid value: stage name is reserved"},
				{"names an approve stage with no review stage", func(p *domain.Pipeline) {
					p.Stages = []domain.Stage{p.Stages[0], p.Stages[1], p.Stages[3]}
				}, domain.ErrInvalidValue, "stage approve: invalid value: approve stage has no review stage before it"},
				{"names an approve stage that comes before review", func(p *domain.Pipeline) {
					p.Stages[2], p.Stages[3] = p.Stages[3], p.Stages[2]
				}, domain.ErrInvalidValue, "stage approve: invalid value: approve stage has no review stage before it"},
				{"rejects a pipeline with no approve stage", func(p *domain.Pipeline) {
					p.Stages = p.Stages[:3]
				}, domain.ErrMissingField, "pipeline default: missing field: approve stage"},
				{"names a second approve stage", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "approve2", Kind: domain.KindApprove})
				}, domain.ErrInvalidValue, "stage approve2: invalid value: second approve stage, want exactly 1"},
				{"names a review stage before the build", func(p *domain.Pipeline) {
					p.Stages = []domain.Stage{
						p.Stages[0],
						{Name: "early-review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "intake"},
						p.Stages[1], p.Stages[2], p.Stages[3],
					}
				}, domain.ErrInvalidValue, "stage early-review: invalid value: review stage comes before the build stage"},
				{"names a review stage after the approve", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "late-review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"})
				}, domain.ErrInvalidValue, "stage late-review: invalid value: review stage comes after the approve stage"},
				{"names a spec stage after the approve", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "spec", Kind: domain.KindSpec})
				}, domain.ErrInvalidValue, "stage spec: invalid value: only task stages may come after the approve stage"},
				{"names an intake stage after the approve", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "intake2", Kind: domain.KindIntake})
				}, domain.ErrInvalidValue, "stage intake2: invalid value: only task stages may come after the approve stage"},
				{"names a non-task stage after a task that follows the approve", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages,
						domain.Stage{Name: "changelog", Kind: domain.KindTask, Roles: []string{"scribe"}},
						domain.Stage{Name: "spec", Kind: domain.KindSpec},
					)
				}, domain.ErrInvalidValue, "stage spec: invalid value: only task stages may come after the approve stage"},
				{"names the approve stage listing the builder", func(p *domain.Pipeline) {
					p.Stages[3].Roles = []string{"builder"}
				}, domain.ErrInvalidValue, `stage approve: invalid value: role "builder" has kind build, want approve`},
				{"names the intake stage listing the builder while it fills build by default", func(p *domain.Pipeline) {
					p.Stages[0].Roles = []string{"builder"}
				}, domain.ErrInvalidValue, `stage intake: invalid value: role "builder" has kind build, want intake`},
				{"names a review stage listing a task role", func(p *domain.Pipeline) {
					p.Stages[2].Roles = []string{"reviewer-code", "scribe"}
				}, domain.ErrInvalidValue, `stage review: invalid value: role "scribe" has kind task, want review`},
				{"names a task stage listing a reviewer", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages, domain.Stage{Name: "changelog", Kind: domain.KindTask, Roles: []string{"reviewer-ux"}})
				}, domain.ErrInvalidValue, `stage changelog: invalid value: role "reviewer-ux" has kind review, want task`},
				{"names the same reviewer in two review stages", func(p *domain.Pipeline) {
					p.Stages = []domain.Stage{
						p.Stages[0], p.Stages[1], p.Stages[2],
						{Name: "second-review", Kind: domain.KindReview, Roles: []string{"reviewer-ux", "reviewer-code"}, OnChanges: "build"},
						p.Stages[3],
					}
				}, domain.ErrInvalidValue, `stage second-review: invalid value: role "reviewer-code" is also in stage review`},
				{"names the same task role in two task stages", func(p *domain.Pipeline) {
					p.Stages = append(p.Stages,
						domain.Stage{Name: "changelog", Kind: domain.KindTask, Roles: []string{"scribe"}},
						domain.Stage{Name: "notify", Kind: domain.KindTask, Roles: []string{"scribe"}},
					)
				}, domain.ErrInvalidValue, `stage notify: invalid value: role "scribe" is also in stage changelog`},
				{"names a default role filling a second stage of its kind", func(p *domain.Pipeline) {
					p.Stages = append([]domain.Stage{{Name: "triage", Kind: domain.KindIntake}}, p.Stages...)
				}, domain.ErrInvalidValue, `stage intake: invalid value: role "intake" is also in stage triage`},
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
				{"names spec_rejections above its cap of 10", func(p *domain.Pipeline) {
					p.Limits.SpecRejections = ptr(11)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: spec_rejections 11, want at most 10"},
				{"names human_reworks above its cap of 10", func(p *domain.Pipeline) {
					p.Limits.HumanReworks = ptr(11)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: human_reworks 11, want at most 10"},
				{"names triage_retries above its cap of 3", func(p *domain.Pipeline) {
					p.Limits.TriageRetries = ptr(4)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: triage_retries 4, want at most 3"},
				{"names max_rounds in limits above its cap of 10", func(p *domain.Pipeline) {
					p.Limits.MaxRounds = ptr(11)
				}, domain.ErrInvalidValue, "pipeline default: limits: invalid value: max_rounds 11, want at most 10"},
				{"names a review with max_rounds above its cap of 10", func(p *domain.Pipeline) {
					p.Stages[2].MaxRounds = ptr(11)
				}, domain.ErrInvalidValue, "stage review: invalid value: max_rounds 11, want at most 10"},
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

type defaultRoleRow struct {
	name  string
	stage domain.Stage
	want  []string
}

func TestPipelineWithDefaultRoles(t *testing.T) {
	specs.Describe(t, "Pipeline.WithDefaultRoles", func(s *specs.Spec) {
		rows := []defaultRoleRow{
			{"gives a roleless intake the intake role", domain.Stage{Name: "intake", Kind: domain.KindIntake}, []string{"intake"}},
			{"gives a roleless spec the spec-writer role", domain.Stage{Name: "spec", Kind: domain.KindSpec}, []string{"spec-writer"}},
			{"gives a roleless build the builder role", domain.Stage{Name: "build", Kind: domain.KindBuild}, []string{"builder"}},
			{"gives a roleless approve the approver role", domain.Stage{Name: "approve", Kind: domain.KindApprove}, []string{"approver"}},
			{"keeps the roles a stage lists", domain.Stage{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder-go"}}, []string{"builder-go"}},
			{"keeps the roles a task lists", domain.Stage{Name: "changelog", Kind: domain.KindTask, Roles: []string{"scribe"}}, []string{"scribe"}},
		}
		specs.Table(s, rows, func(r defaultRoleRow) string { return r.name }, func(ctx *specs.Context, r defaultRoleRow) {
			p := domain.Pipeline{Name: "p", Stages: []domain.Stage{r.stage}}

			got, err := p.WithDefaultRoles()

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got.Stages[0].Roles).ToEqual(r.want)
		})

		s.It("leaves the pipeline it was called on unchanged", func(ctx *specs.Context) {
			p := validPipeline()

			_, err := p.WithDefaultRoles()

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(p).ToEqual(validPipeline())
		})

		s.It("names a task stage that lists no roles, since task has no default", func(ctx *specs.Context) {
			p := validPipeline()
			p.Stages = append(p.Stages, domain.Stage{Name: "changelog", Kind: domain.KindTask})

			_, err := p.WithDefaultRoles()

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(err.Error()).ToEqual("stage changelog: missing field: kind task lists no roles and has no default role")
		})

		s.It("names a review stage that lists no roles, since review has no default", func(ctx *specs.Context) {
			p := validPipeline()
			p.Stages[2].Roles = nil

			_, err := p.WithDefaultRoles()

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(err.Error()).ToEqual("stage review: missing field: kind review lists no roles and has no default role")
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

func TestPipelineHumanReworks(t *testing.T) {
	specs.Describe(t, "Pipeline.HumanReworks", func(s *specs.Spec) {
		s.It("is unlimited when limits does not set it", func(ctx *specs.Context) {
			_, capped := validPipeline().HumanReworks()
			ctx.Expect(capped).ToEqual(false)
		})

		s.It("is limits.human_reworks when set", func(ctx *specs.Context) {
			p := validPipeline()
			p.Limits.HumanReworks = ptr(2)
			n, capped := p.HumanReworks()
			ctx.Expect(n).ToEqual(2)
			ctx.Expect(capped).ToEqual(true)
		})
	})
}

type maxRoundsRow struct {
	name   string
	stage  *int
	limits *int
	want   int
}

func TestPipelineMaxRounds(t *testing.T) {
	specs.Describe(t, "Pipeline.MaxRounds", func(s *specs.Spec) {
		rows := []maxRoundsRow{
			{"is 3 when neither the stage nor limits sets it", nil, nil, 3},
			{"is limits.max_rounds when the stage does not set it", nil, ptr(2), 2},
			{"is the stage's max_rounds when limits does not set it", ptr(4), nil, 4},
			{"is the stage's max_rounds over a different limits.max_rounds", ptr(4), ptr(2), 4},
		}
		specs.Table(s, rows, func(r maxRoundsRow) string { return r.name }, func(ctx *specs.Context, r maxRoundsRow) {
			p := validPipeline()
			p.Stages[2].MaxRounds = r.stage
			p.Limits.MaxRounds = r.limits
			ctx.Expect(p.MaxRounds(p.Stages[2])).ToEqual(r.want)
		})
	})
}
