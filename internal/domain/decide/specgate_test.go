package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

type specgateRow struct {
	name     string
	rejected int
	want     []domain.Event
}

func atSpecgate(rejected int) domain.Job {
	job := newJob()
	job.Stage, job.Round, job.SpecRejections = domain.StageSpecgate, 2, rejected
	return job
}

func specgateFailed(minute int) domain.SpecgateFailed {
	return domain.SpecgateFailed{Job: jobID, At: at(minute)}
}

func rejected(n, minute int) domain.SpecRejected {
	return domain.SpecRejected{Job: jobID, Rejections: n, At: at(minute)}
}

func TestDecideSpecgate(t *testing.T) {
	specs.Describe(t, "Decide on a specgate rejection", func(s *specs.Spec) {
		s.When("specgate rejects the spec at specgate", func(s *specs.Spec) {
			rows := []specgateRow{
				{"the first rejection goes back to spec and counts 1", 0,
					[]domain.Event{rejected(1, 1), entered("spec", 1)}},
				{"the second rejection goes back to spec and counts 2", 1,
					[]domain.Event{rejected(2, 1), entered("spec", 1)}},
				{"the third rejection ends in needs-human, marked as specgate's escalation", 2,
					[]domain.Event{rejected(3, 1), domain.NeedsHuman{Job: jobID, Reason: "spec rejected by specgate 3 times", Cause: domain.CauseSpecEscalated, At: at(1)}}},
			}
			specs.Table(s, rows, func(r specgateRow) string { return r.name }, func(ctx *specs.Context, r specgateRow) {
				job := atSpecgate(r.rejected)

				evs, err := decide.Decide(job, specgateFailed(1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
				after := decide.Apply(job, evs)
				ctx.Expect(after.SpecRejections).ToEqual(r.rejected + 1)
				ctx.Expect(after.Round).ToEqual(job.Round)
			})
		})

		s.When("specgate rejects the spec outside specgate", func(s *specs.Spec) {
			stages := []string{"intake", "spec", "build", domain.StageGate, "review", "approve", domain.StageMerge, domain.StageNeedsHuman}
			specs.Table(s, stages, func(st string) string { return "in " + st + " is refused" }, func(ctx *specs.Context, st string) {
				job, before := atSpecgate(1), atSpecgate(1)
				job.Stage, before.Stage = st, st

				evs, err := decide.Decide(job, specgateFailed(1), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(before)
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.It("rewrites the spec twice and stops at needs-human on the third rejection, in the same round", func(ctx *specs.Context) {
			p := defaultPipeline()
			job := newJob()
			job.Stage = "spec"
			step := func(ev domain.Event) {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}

			for k := 1; k <= 2; k++ {
				step(result("spec-writer", 2*k, domain.SpecResult{Title: "S6", Spec: "# S6"}))
				ctx.Expect(job.Stage).ToEqual(domain.StageSpecgate)
				step(specgateFailed(2*k + 1))
				ctx.Expect(job.Stage).ToEqual("spec")
				ctx.Expect(job.SpecRejections).ToEqual(k)
			}
			step(result("spec-writer", 6, domain.SpecResult{Title: "S6", Spec: "# S6"}))
			step(specgateFailed(7))

			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(job.SpecRejections).ToEqual(3)
			ctx.Expect(job.Round).ToEqual(1)
		})
	})
}
