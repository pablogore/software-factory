package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// failedAttempt is a builder's session that ended twice without a result.
func failedAttempt(minute int) domain.AttemptFailed {
	return domain.AttemptFailed{Job: jobID, Role: "builder", Reason: "no result submitted", At: at(minute)}
}

type attemptRow struct {
	name string
	ev   func() domain.AttemptFailed
	want error
}

func TestAttemptFailed(t *testing.T) {
	specs.Describe(t, "Decide on a failed attempt", func(s *specs.Spec) {
		s.It("records it in the job's history and leaves the job where it is", func(ctx *specs.Context) {
			job := building(1)

			evs, err := decide.Decide(job, failedAttempt(1), defaultPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{failedAttempt(1)})
			ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
		})

		s.It("is refused on a stopped job", func(ctx *specs.Context) {
			job := building(1)
			job.Stage = domain.StageStopped

			evs, err := decide.Decide(job, failedAttempt(1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		rows := []attemptRow{
			{"without a reason", func() domain.AttemptFailed { e := failedAttempt(1); e.Reason = " "; return e }, domain.ErrMissingField},
			{"without a role", func() domain.AttemptFailed { e := failedAttempt(1); e.Role = ""; return e }, domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r attemptRow) string { return "is refused " + r.name }, func(ctx *specs.Context, r attemptRow) {
			evs, err := decide.Decide(building(1), r.ev(), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(evs).To(specs.BeEmpty())
		})
	})
}
