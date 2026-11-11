package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// whenPipeline is a pipeline whose review stage has reviewer-code, with no
// when, reviewer-ui, which runs on ui/**, and reviewer-big, which runs on a
// diff of 200 lines or more, or on the security label.
func whenPipeline() domain.Pipeline {
	return domain.Pipeline{
		Name: "when",
		Stages: []domain.Stage{
			{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
			{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code", "reviewer-ui", "reviewer-big"}, OnChanges: "build", MaxRounds: new(3)},
			{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
		},
		Roles: []domain.Role{
			{Name: "reviewer-code", Kind: domain.KindReview, Mode: domain.ModeBlocking},
			{Name: "reviewer-ui", Kind: domain.KindReview, Mode: domain.ModeBlocking, When: domain.When{Any: []domain.Condition{{Paths: []string{"ui/**"}}}}},
			{Name: "reviewer-big", Kind: domain.KindReview, Mode: domain.ModeBlocking, When: domain.When{Any: []domain.Condition{{DiffLines: ">= 200"}}}},
		},
	}
}

func gatePassed(minute int, diff *domain.DiffSummary) domain.GatePassed {
	return domain.GatePassed{Job: jobID, Run: passingRun, Diff: diff, At: at(minute)}
}

func skippedRole(role, reason string, round, minute int) domain.ReviewSkipped {
	return domain.ReviewSkipped{Job: jobID, Round: round, Role: role, Reason: reason, At: at(minute)}
}

const (
	noUI  = "when does not apply: no changed file matches paths ui/**"
	small = "when does not apply: the diff has 12 lines, not >= 200"
)

type skipRow struct {
	name string
	diff *domain.DiffSummary
	want []domain.Event
}

func TestReviewSkip(t *testing.T) {
	specs.Describe(t, "Decide on a passing gate before a review stage", func(s *specs.Spec) {
		rows := []skipRow{
			{"runs every role when the diff meets each when", &domain.DiffSummary{Paths: []string{"ui/app.tsx"}, Lines: 250},
				[]domain.Event{ran(1, 1, passingRun), entered("review", 1)}},
			{"skips a role whose paths match no changed file, with its name and reason", &domain.DiffSummary{Paths: []string{"internal/a.go"}, Lines: 250},
				[]domain.Event{ran(1, 1, passingRun), entered("review", 1), skippedRole("reviewer-ui", noUI, 1, 1)}},
			{"skips a role whose diff_lines the diff size does not meet", &domain.DiffSummary{Paths: []string{"ui/a/b.tsx"}, Lines: 12},
				[]domain.Event{ran(1, 1, passingRun), entered("review", 1), skippedRole("reviewer-big", small, 1, 1)}},
			{"runs every role when the gate could not tell the diff", nil,
				[]domain.Event{ran(1, 1, passingRun), entered("review", 1)}},
		}
		specs.Table(s, rows, func(r skipRow) string { return r.name }, func(ctx *specs.Context, r skipRow) {
			evs, err := decide.Decide(atGate(1, nil), gatePassed(1, r.diff), whenPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual(r.want)
		})

		s.It("runs a role whose when depends on labels, which the job can't evaluate yet", func(ctx *specs.Context) {
			p := whenPipeline()
			p.Roles[2].When.Any = append(p.Roles[2].When.Any, domain.Condition{Labels: []string{"security"}})

			evs, err := decide.Decide(atGate(1, nil), gatePassed(1, &domain.DiffSummary{Paths: []string{"ui/a.tsx"}, Lines: 12}), p)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ran(1, 1, passingRun), entered("review", 1)})
		})

		s.It("skips a role whose risk the intake rating doesn't meet, with its reason, and runs it on a job intake never rated", func(ctx *specs.Context) {
			p := whenPipeline()
			p.Roles[0].When.Any = []domain.Condition{{Risk: []domain.Risk{domain.RiskHigh}}}
			diff := &domain.DiffSummary{Paths: []string{"ui/a.tsx"}, Lines: 250}
			low := atGate(1, nil)
			low.Risk = domain.RiskLow

			evs, err := decide.Decide(low, gatePassed(1, diff), p)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ran(1, 1, passingRun), entered("review", 1),
				skippedRole("reviewer-code", "when does not apply: the intake risk is low, not high", 1, 1)})

			evs, err = decide.Decide(atGate(1, nil), gatePassed(1, diff), p)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ran(1, 1, passingRun), entered("review", 1)})
		})

		s.It("skips a role whose surfaces the intake shares none of, and runs it when it shares one", func(ctx *specs.Context) {
			p := whenPipeline()
			p.Roles[0].When.Any = []domain.Condition{{Surfaces: []string{"auth"}}}
			diff := &domain.DiffSummary{Paths: []string{"ui/a.tsx"}, Lines: 250}
			job := atGate(1, nil)
			job.Risk, job.Surfaces = domain.RiskMedium, []string{"ui"}

			evs, err := decide.Decide(job, gatePassed(1, diff), p)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ran(1, 1, passingRun), entered("review", 1),
				skippedRole("reviewer-code", "when does not apply: no intake surface matches surfaces auth", 1, 1)})

			job.Surfaces = []string{"ui", "auth"}
			evs, err = decide.Decide(job, gatePassed(1, diff), p)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ran(1, 1, passingRun), entered("review", 1)})
		})

		s.It("judges only the round's reviewers in a later round", func(ctx *specs.Context) {
			evs, err := decide.Decide(atGate(2, []string{"reviewer-code", "reviewer-ui"}), gatePassed(1, &domain.DiffSummary{Paths: []string{"a.go"}, Lines: 1}), whenPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ran(2, 1, passingRun), entered("review", 1), skippedRole("reviewer-ui", noUI, 2, 1)})
		})

		s.It("moves past the review stage when every role of the round is skipped", func(ctx *specs.Context) {
			evs, err := decide.Decide(atGate(2, []string{"reviewer-ui"}), gatePassed(1, &domain.DiffSummary{Paths: []string{"a.go"}, Lines: 1}), whenPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{ran(2, 1, passingRun), entered("review", 1), skippedRole("reviewer-ui", noUI, 2, 1), entered("approve", 1)})
		})

		s.It("keeps the skipped roles on the job for the stage and round, and forgets them once it leaves", func(ctx *specs.Context) {
			job := atGate(1, nil)
			evs, err := decide.Decide(job, gatePassed(1, &domain.DiffSummary{Paths: []string{"a.go"}, Lines: 1}), whenPipeline())
			ctx.Expect(err).To(specs.BeNil())

			job = decide.Apply(job, evs)
			ctx.Expect(job.Skipped).ToEqual([]string{"reviewer-ui", "reviewer-big"})

			job = decide.Apply(job, []domain.Event{entered("approve", 2)})
			ctx.Expect(job.Skipped).To(specs.BeNil())
		})
	})

	specs.Describe(t, "Decide on a review round with skipped roles", func(s *specs.Spec) {
		skippedUI := func() domain.Job {
			job := newJob()
			job.Stage, job.Skipped = "review", []string{"reviewer-ui"}
			return job
		}

		s.It("ends the round on the verdicts of the roles that ran, without waiting for a skipped one", func(ctx *specs.Context) {
			job := skippedUI()
			job.Passed = []string{"reviewer-code"}

			evs, err := decide.Decide(job, pass("reviewer-big", 2), whenPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{passed("reviewer-big", 2), entered("approve", 2)})
		})

		s.It("waits for every role that ran", func(ctx *specs.Context) {
			evs, err := decide.Decide(skippedUI(), pass("reviewer-big", 2), whenPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{passed("reviewer-big", 2)})
		})

		s.It("runs a skipped role again in the next round, since it has no pass to keep", func(ctx *specs.Context) {
			job := skippedUI()
			job.Passed = []string{"reviewer-code"}

			evs, err := decide.Decide(job, changes("reviewer-big", 2), whenPipeline())

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{asked("reviewer-big", 2), started(2, 2, "reviewer-ui", "reviewer-big"), entered("build", 2)})
		})

		s.It("refuses a verdict from a skipped role", func(ctx *specs.Context) {
			_, err := decide.Decide(skippedUI(), pass("reviewer-ui", 2), whenPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
		})
	})
}
