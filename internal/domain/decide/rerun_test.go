package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// threeReviewers is a pipeline whose review stage has three reviewers, with
// reviewer-security marked always_rerun.
func threeReviewers() domain.Pipeline {
	return domain.Pipeline{
		Name: "three",
		Stages: []domain.Stage{
			{Name: "intake", Kind: domain.KindIntake, Roles: []string{"intake"}},
			{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
			{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
			{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-security", "reviewer-ux", "reviewer-code"}, OnChanges: "build", MaxRounds: new(3)},
			{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
		},
		Roles: []domain.Role{
			{Name: "reviewer-security", Kind: domain.KindReview, Mode: domain.ModeBlocking, AlwaysRerun: true},
			{Name: "reviewer-ux", Kind: domain.KindReview, Mode: domain.ModeBlocking},
			{Name: "reviewer-code", Kind: domain.KindReview, Mode: domain.ModeBlocking},
		},
	}
}

type rerunRow struct {
	name      string
	round     int
	reviewers []string
	passed    []string
	changed   []string
	ev        domain.ResultSubmitted
	want      []domain.Event
}

func reviewing(r rerunRow) domain.Job {
	job := newJob()
	job.Stage, job.Round, job.Reviewers, job.Passed, job.Changes = "review", r.round, r.reviewers, r.passed, r.changed
	return job
}

func TestDecideRerun(t *testing.T) {
	specs.Describe(t, "Decide in rounds after the first", func(s *specs.Spec) {
		secCode := []string{"reviewer-security", "reviewer-code"}

		s.When("a round ends with CHANGES", func(s *specs.Spec) {
			rows := []rerunRow{
				{"round 2 lists the reviewer that asked for CHANGES plus the always_rerun one", 1, nil, []string{"reviewer-security", "reviewer-ux"}, nil, changes("reviewer-code", 1),
					[]domain.Event{asked("reviewer-code", 1), started(2, 1, secCode...), entered("build", 1)}},
				{"round 2 lists every reviewer that asked for CHANGES in stage order", 1, nil, []string{"reviewer-security"}, []string{"reviewer-code"}, changes("reviewer-ux", 1),
					[]domain.Event{asked("reviewer-ux", 1), started(2, 1, "reviewer-security", "reviewer-ux", "reviewer-code"), entered("build", 1)}},
				{"round 3 lists the round 2 reviewer that asked for CHANGES plus the always_rerun one", 2, secCode, []string{"reviewer-security"}, nil, changes("reviewer-code", 1),
					[]domain.Event{asked("reviewer-code", 1), started(3, 1, secCode...), entered("build", 1)}},
			}
			specs.Table(s, rows, func(r rerunRow) string { return r.name }, func(ctx *specs.Context, r rerunRow) {
				evs, err := decide.Decide(reviewing(r), r.ev, threeReviewers())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})

			s.It("lists only the reviewer that asked for CHANGES when no role is always_rerun", func(ctx *specs.Context) {
				job := newJob()
				job.Stage, job.Passed = "review", []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "security-deep"}

				evs, err := decide.Decide(job, changes("reviewer-code", 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{asked("reviewer-code", 1), started(2, 1, "reviewer-code"), entered("build", 1)})
			})

			s.It("lists a shadow reviewer that asked for CHANGES in the next round", func(ctx *specs.Context) {
				job := newJob()
				job.Stage, job.Passed, job.Changes = "review", []string{"reviewer-security", "reviewer-ux", "reviewer-ui"}, []string{"security-deep"}

				evs, err := decide.Decide(job, changes("reviewer-code", 1), modesPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{asked("reviewer-code", 1), started(2, 1, "reviewer-code", "security-deep"), entered("build", 1)})
			})
		})

		s.When("reviewers answer in round 2 or later", func(s *specs.Spec) {
			rows := []rerunRow{
				{"the first of the round's reviewers waits for the other", 2, secCode, nil, nil, pass("reviewer-security", 1),
					[]domain.Event{passed("reviewer-security", 1)}},
				{"the last of the round's reviewers passing enters approve", 2, secCode, []string{"reviewer-security"}, nil, pass("reviewer-code", 1),
					[]domain.Event{passed("reviewer-code", 1), entered("approve", 1)}},
				{"the round's reviewers in any order enter approve", 2, secCode, []string{"reviewer-code"}, nil, pass("reviewer-security", 1),
					[]domain.Event{passed("reviewer-security", 1), entered("approve", 1)}},
				{"CHANGES from the round's last reviewer in round 3 of 3 ends in needs-human", 3, secCode, []string{"reviewer-code"}, nil, changes("reviewer-security", 1),
					[]domain.Event{asked("reviewer-security", 1), domain.NeedsHuman{Job: jobID,
						Reason: "CHANGES in the last round (3 of 3) from reviewer-security", Cause: domain.CauseChanges, At: at(1)}}},
				{"round 1 ends once every reviewer of the stage answered", 1, nil, secCode, nil, pass("reviewer-ux", 1),
					[]domain.Event{passed("reviewer-ux", 1), entered("approve", 1)}},
				{"round 1 counts CHANGES from a reviewer that is not always_rerun", 1, nil, secCode, nil, changes("reviewer-ux", 1),
					[]domain.Event{asked("reviewer-ux", 1), started(2, 1, "reviewer-security", "reviewer-ux"), entered("build", 1)}},
			}
			specs.Table(s, rows, func(r rerunRow) string { return r.name }, func(ctx *specs.Context, r rerunRow) {
				evs, err := decide.Decide(reviewing(r), r.ev, threeReviewers())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})

			s.It("does not end round 1 while a reviewer of the stage has not answered", func(ctx *specs.Context) {
				job := reviewing(rerunRow{round: 1, passed: []string{"reviewer-security"}})

				evs, err := decide.Decide(job, pass("reviewer-code", 1), threeReviewers())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{passed("reviewer-code", 1)})
			})
		})

		s.When("a reviewer outside the round answers", func(s *specs.Spec) {
			rows := []rerunRow{
				{"a PASS before anyone answered is refused", 2, secCode, nil, nil, pass("reviewer-ux", 1), nil},
				{"a CHANGES before anyone answered is refused", 2, secCode, nil, nil, changes("reviewer-ux", 1), nil},
				{"a PASS that would complete the stage is refused", 2, secCode, []string{"reviewer-security"}, []string{"reviewer-code"}, pass("reviewer-ux", 1), nil},
				{"a CHANGES in round 3 is refused", 3, []string{"reviewer-code"}, nil, nil, changes("reviewer-security", 1), nil},
			}
			specs.Table(s, rows, func(r rerunRow) string { return r.name }, func(ctx *specs.Context, r rerunRow) {
				job, before := reviewing(r), reviewing(r)

				evs, err := decide.Decide(job, r.ev, threeReviewers())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(before)
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.It("walks round 1 with three reviewers into a round 2 of two that enters approve", func(ctx *specs.Context) {
			p := threeReviewers()
			job := newJob()
			job.Stage = "review"
			step := func(ev domain.Event) {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}
			for _, ev := range []domain.Event{pass("reviewer-security", 1), pass("reviewer-ux", 2), changes("reviewer-code", 3)} {
				step(ev)
			}
			ctx.Expect(job.Stage).ToEqual("build")
			ctx.Expect(job.Round).ToEqual(2)
			ctx.Expect(job.Reviewers).ToEqual(secCode)

			step(result("builder", 4, domain.BuildResult{Status: domain.BuildReady}))
			step(domain.GatePassed{Job: jobID, At: at(5)})
			ctx.Expect(job.Stage).ToEqual("review")
			ctx.Expect(job.Reviewers).ToEqual(secCode)

			evs, err := decide.Decide(job, pass("reviewer-ux", 6), p)
			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			ctx.Expect(evs).To(specs.BeEmpty())

			step(pass("reviewer-code", 7))
			step(pass("reviewer-security", 8))
			ctx.Expect(job.Stage).ToEqual("approve")
			ctx.Expect(job.Round).ToEqual(2)
		})

		s.It("forgets the round's reviewers when the job enters needs-human", func(ctx *specs.Context) {
			job := reviewing(rerunRow{round: 3, reviewers: secCode, passed: []string{"reviewer-code"}})

			evs, err := decide.Decide(job, changes("reviewer-security", 1), threeReviewers())
			ctx.Expect(err).To(specs.BeNil())
			job = decide.Apply(job, evs)

			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(job.Reviewers).To(specs.BeEmpty())
		})
	})
}
