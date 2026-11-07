package decide_test

import (
	"fmt"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

type maxRoundsRow struct {
	name   string
	stage  *int
	limits *int
	round  int
	want   []domain.Event
}

// codeChangesIn is a review job in round where every reviewer but
// reviewer-code has passed, so its CHANGES closes the round.
func codeChangesIn(round int) domain.Job {
	job := newJob()
	job.Stage, job.Round, job.Passed = "review", round, []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "security-deep"}
	return job
}

func lastRound(round, limit int) []domain.Event {
	return []domain.Event{asked("reviewer-code", 1),
		triaging(fmt.Sprintf("CHANGES in the last round (%d of %d) from reviewer-code", round, limit), domain.CauseChanges, 1)}
}

func anotherRound(round int) []domain.Event {
	return []domain.Event{asked("reviewer-code", 1), started(round+1, 1, "reviewer-code"), entered("build", 1)}
}

func TestDecideMaxRounds(t *testing.T) {
	two, four := 2, 4
	specs.Describe(t, "Decide on review rounds", func(s *specs.Spec) {
		s.When("CHANGES closes a round", func(s *specs.Spec) {
			rows := []maxRoundsRow{
				{"with limits.max_rounds 2 and no stage value, round 1 goes back to build", nil, &two, 1, anotherRound(1)},
				{"with limits.max_rounds 2 and no stage value, round 2 goes to triage", nil, &two, 2, lastRound(2, 2)},
				{"with stage max_rounds 4 over limits.max_rounds 2, round 2 goes back to build", &four, &two, 2, anotherRound(2)},
				{"with stage max_rounds 4 over limits.max_rounds 2, round 4 goes to triage", &four, &two, 4, lastRound(4, 4)},
				{"with no limits.max_rounds and no stage value, round 2 goes back to build", nil, nil, 2, anotherRound(2)},
				{"with no limits.max_rounds and no stage value, round 3 goes to triage", nil, nil, 3, lastRound(3, 3)},
			}
			specs.Table(s, rows, func(r maxRoundsRow) string { return r.name }, func(ctx *specs.Context, r maxRoundsRow) {
				p := defaultPipeline()
				p.Stages[3].MaxRounds = r.stage
				p.Limits.MaxRounds = r.limits

				evs, err := decide.Decide(codeChangesIn(r.round), changes("reviewer-code", 1), p)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})
		})

		s.When("the gate fails", func(s *specs.Spec) {
			rows := []maxRoundsRow{
				{"with limits.max_rounds 2 and no stage value, round 2 goes to triage", nil, &two, 2,
					[]domain.Event{triaging("gate failed in the last round (2 of 2)", domain.CauseGateFailed, 1)}},
				{"with stage max_rounds 4 over limits.max_rounds 2, round 2 goes back to build", &four, &two, 2,
					[]domain.Event{started(3, 1), entered("build", 1)}},
			}
			specs.Table(s, rows, func(r maxRoundsRow) string { return r.name }, func(ctx *specs.Context, r maxRoundsRow) {
				p := defaultPipeline()
				p.Stages[3].MaxRounds = r.stage
				p.Limits.MaxRounds = r.limits

				evs, err := decide.Decide(atGate(r.round, nil), gateFailed(1), p)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})
		})
	})
}
