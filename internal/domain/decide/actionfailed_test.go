package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// refused is the outbox's report that the destination refused the job's
// comment for permission.
func refused(minute int) domain.ActionFailed {
	return domain.ActionFailed{Job: jobID, Key: "job-1/comment/1", Kind: "comment", Error: "403 forbidden", At: at(minute)}
}

const refusedReason = `external action comment "job-1/comment/1" failed: 403 forbidden`

type actionStageRow struct {
	name  string
	stage string
}

func TestActionFailed(t *testing.T) {
	specs.Describe(t, "Decide on a refused external action", func(s *specs.Spec) {
		live := []actionStageRow{
			{"in intake", "intake"}, {"in spec", "spec"}, {"in specgate", domain.StageSpecgate}, {"in build", "build"},
			{"in the gate", domain.StageGate}, {"in review", "review"}, {"in approve", "approve"}, {"in merge", domain.StageMerge},
			{"in done", domain.StageDone}, {"in needs-info", domain.StageNeedsInfo},
		}
		specs.Table(s, live, func(r actionStageRow) string { return "sends the job to needs-human with action-failed " + r.name }, func(ctx *specs.Context, r actionStageRow) {
			job := newJob()
			job.Stage = r.stage

			evs, err := decide.Decide(job, refused(1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: refusedReason, Cause: domain.CauseActionFailed, At: at(1)}})
			after := decide.Apply(job, evs)
			ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(after.Cause).ToEqual(domain.CauseActionFailed)
			ctx.Expect(after.Validate()).To(specs.BeNil())
		})

		s.It("on a job already in needs-human, waits again for the refusal so an OK for the old cause is stale", func(ctx *specs.Context) {
			job := waitingFor(ctx, domain.CauseEscalated)

			evs, err := decide.Decide(job, refused(2), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			after := decide.Apply(job, evs)
			ctx.Expect(after.Cause).ToEqual(domain.CauseActionFailed)
			ctx.Expect(after.WaitSeq).ToEqual(job.WaitSeq + 1)
			_, err = decide.Decide(after, domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: job.WaitSeq, At: at(3)}, defaultPipeline())
			ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
		})

		ended := []actionStageRow{{"on a stopped job", domain.StageStopped}, {"on a rejected job", domain.StageRejected}}
		specs.Table(s, ended, func(r actionStageRow) string { return "changes nothing and is not an error " + r.name }, func(ctx *specs.Context, r actionStageRow) {
			job := newJob()
			job.Stage = r.stage

			evs, err := decide.Decide(job, refused(1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("without the action's key is refused", func(ctx *specs.Context) {
			ev := refused(1)
			ev.Key = " "

			evs, err := decide.Decide(building(1), ev, defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(evs).To(specs.BeEmpty())
		})
	})
}
