package decide_test

import (
	"errors"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// taskPipeline is defaultPipeline with a task stage after approve, so every
// kind has a stage to submit its result in.
func taskPipeline() domain.Pipeline {
	p := defaultPipeline()
	p.Stages = append(p.Stages, domain.Stage{Name: "changelog", Kind: domain.KindTask, Roles: []string{"scribe"}})
	return p
}

type kindRow struct {
	name        string
	stage       string
	role        string
	result      domain.Result
	unsupported bool
}

func TestKinds(t *testing.T) {
	specs.Describe(t, "Decide on every kind's results", func(s *specs.Spec) {
		rows := []kindRow{
			{"decides an intake ACCEPT", "intake", "intake", domain.IntakeResult{Decision: domain.IntakeAccept}, false},
			{"decides an intake NEEDS_INFO", "intake", "intake", domain.IntakeResult{Decision: domain.IntakeNeedsInfo}, false},
			{"decides an intake REJECT", "intake", "intake", domain.IntakeResult{Decision: domain.IntakeReject}, false},
			{"decides a spec", "spec", "spec-writer", domain.SpecResult{Title: "S1", Spec: "# S1"}, false},
			{"decides a builder READY", "build", "builder", domain.BuildResult{Status: domain.BuildReady}, false},
			{"decides a builder BLOCKED", "build", "builder", domain.BuildResult{Status: domain.BuildBlocked}, false},
			{"decides a review PASS", "review", "reviewer-code", domain.ReviewResult{Verdict: domain.ReviewPass}, false},
			{"decides a review CHANGES", "review", "reviewer-code", domain.ReviewResult{Verdict: domain.ReviewChanges}, false},
			{"decides an approver APPROVE", "approve", "approver", domain.ApproveResult{Decision: domain.ApproveApprove}, false},
			{"decides an approver ESCALATE", "approve", "approver", domain.ApproveResult{Decision: domain.ApproveEscalate}, false},
			{"leaves a task result unsupported (#76)", "changelog", "scribe", domain.TaskResult{Produces: []string{"CHANGELOG.md"}}, true},
		}
		specs.Table(s, rows, func(r kindRow) string { return r.name }, func(ctx *specs.Context, r kindRow) {
			job := newJob()
			job.Stage = r.stage

			evs, err := decide.Decide(job, result(r.role, 1, r.result), taskPipeline())

			ctx.Expect(errors.Is(err, decide.ErrUnsupported)).ToEqual(r.unsupported)
			if r.unsupported {
				ctx.Expect(evs).To(specs.BeEmpty())
				return
			}
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(len(evs) > 0).ToEqual(true)
		})

		s.It("covers every kind", func(ctx *specs.Context) {
			var kinds []domain.Kind
			for _, r := range rows {
				if len(kinds) == 0 || kinds[len(kinds)-1] != r.result.Kind() {
					kinds = append(kinds, r.result.Kind())
				}
			}
			ctx.Expect(kinds).ToEqual(domain.Kinds)
		})
	})
}
