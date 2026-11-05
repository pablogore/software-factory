package decide_test

import (
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const jobID domain.JobID = "003-e01-3-decide"

// reviewers are the review roles of the default pipeline in 04-roles.md.
var reviewers = []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "reviewer-code", "security-deep"}

// defaultPipeline is pipelines/default.yaml of 04-roles.md, built by hand
// until loading YAML (#24) exists.
func defaultPipeline() domain.Pipeline {
	return domain.Pipeline{
		Name: "default",
		Stages: []domain.Stage{
			{Name: "intake", Kind: domain.KindIntake, Roles: []string{"intake"}},
			{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
			{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
			{Name: "review", Kind: domain.KindReview, Roles: reviewers, OnChanges: "build", MaxRounds: 3},
			{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
		},
	}
}

func newJob() domain.Job {
	return domain.Job{ID: jobID, Title: "Decide happy path", Pipeline: "default", Stage: "intake", Round: 1}
}

func at(minute int) time.Time {
	return time.Date(2026, 11, 5, 10, minute, 0, 0, time.UTC)
}

func result(role string, minute int, r domain.Result) domain.ResultSubmitted {
	return domain.ResultSubmitted{Job: jobID, Role: role, Result: r, At: at(minute)}
}

func pass(role string, minute int) domain.ResultSubmitted {
	return result(role, minute, domain.ReviewResult{Verdict: domain.ReviewPass})
}

func changes(role string, minute int) domain.ResultSubmitted {
	return result(role, minute, domain.ReviewResult{Verdict: domain.ReviewChanges})
}

func asked(role string, minute int) domain.ChangesRequested {
	return domain.ChangesRequested{Job: jobID, Role: role, At: at(minute)}
}

func entered(stage string, minute int) domain.StageEntered {
	return domain.StageEntered{Job: jobID, Stage: stage, At: at(minute)}
}

func passed(role string, minute int) domain.ReviewPassed {
	return domain.ReviewPassed{Job: jobID, Role: role, At: at(minute)}
}

func message(kind domain.AuthorKind, allowed bool, minute int) domain.MessageReceived {
	return domain.MessageReceived{Job: jobID, Provider: "github", Thread: "pr/88", Author: "pablogore",
		AuthorKind: kind, Allowed: allowed, Text: "rename the flag", At: at(minute)}
}

type step struct {
	name string
	ev   domain.Event
	want []domain.Event
}

func happyPath() []step {
	return []step{
		{"intake ACCEPT enters spec", result("intake", 1, domain.IntakeResult{Decision: domain.IntakeAccept}),
			[]domain.Event{entered("spec", 1)}},
		{"spec READY enters specgate", result("spec-writer", 2, domain.SpecResult{Title: "S1", Spec: "# S1"}),
			[]domain.Event{entered(domain.StageSpecgate, 2)}},
		{"specgate OK enters build", domain.SpecgatePassed{Job: jobID, At: at(3)},
			[]domain.Event{entered("build", 3)}},
		{"build READY enters gate", result("builder", 4, domain.BuildResult{Status: domain.BuildReady}),
			[]domain.Event{entered(domain.StageGate, 4)}},
		{"gate OK enters review", domain.GatePassed{Job: jobID, At: at(5)},
			[]domain.Event{entered("review", 5)}},
		{"reviewer-security PASS", pass("reviewer-security", 6), []domain.Event{passed("reviewer-security", 6)}},
		{"reviewer-ux PASS", pass("reviewer-ux", 7), []domain.Event{passed("reviewer-ux", 7)}},
		{"reviewer-ui PASS", pass("reviewer-ui", 8), []domain.Event{passed("reviewer-ui", 8)}},
		{"reviewer-code PASS", pass("reviewer-code", 9), []domain.Event{passed("reviewer-code", 9)}},
		{"security-deep PASS enters approve", pass("security-deep", 10),
			[]domain.Event{passed("security-deep", 10), entered("approve", 10)}},
		{"APPROVE requests the merge", result("approver", 11, domain.ApproveResult{Decision: domain.ApproveApprove}),
			[]domain.Event{domain.MergeRequested{Job: jobID, At: at(11)}}},
	}
}

// walk applies the first n steps of the happy path to a new job.
func walk(ctx *specs.Context, steps []step, n int) domain.Job {
	job, p := newJob(), defaultPipeline()
	for _, st := range steps[:n] {
		evs, err := decide.Decide(job, st.ev, p)
		ctx.Expect(err).To(specs.BeNil())
		job = decide.Apply(job, evs)
	}
	return job
}

type illegalRow struct {
	name  string
	stage string
	ev    domain.Event
}

type messageRow struct {
	name string
	ev   domain.MessageReceived
}

type reviewRow struct {
	name    string
	round   int
	passed  []string
	changed []string
	ev      domain.ResultSubmitted
	want    []domain.Event
}

func TestDecide(t *testing.T) {
	specs.Describe(t, "Decide", func(s *specs.Spec) {
		s.When("walking the default pipeline's happy path", func(s *specs.Spec) {
			steps := happyPath()
			type indexed struct {
				i int
				step
			}
			rows := make([]indexed, len(steps))
			for i, st := range steps {
				rows[i] = indexed{i, st}
			}
			specs.Table(s, rows, func(r indexed) string { return r.name }, func(ctx *specs.Context, r indexed) {
				job := walk(ctx, steps, r.i)

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})

			s.It("ends in merge requested", func(ctx *specs.Context) {
				job := walk(ctx, steps, len(steps))

				ctx.Expect(job.Stage).ToEqual(domain.StageMerge)
				ctx.Expect(job.Passed).To(specs.BeEmpty())
			})
		})

		s.When("the event does not fit the current stage", func(s *specs.Spec) {
			rows := []illegalRow{
				{"review result while in spec", "spec", pass("reviewer-code", 1)},
				{"spec result while in review", "review", result("spec-writer", 1, domain.SpecResult{Spec: "# S1"})},
				{"intake result while in approve", "approve", result("intake", 1, domain.IntakeResult{Decision: domain.IntakeAccept})},
				{"build result while in specgate", domain.StageSpecgate, result("builder", 1, domain.BuildResult{Status: domain.BuildReady})},
				{"specgate OK while in build", "build", domain.SpecgatePassed{Job: jobID, At: at(1)}},
				{"gate OK while in specgate", domain.StageSpecgate, domain.GatePassed{Job: jobID, At: at(1)}},
				{"review from a role outside the stage", "review", pass("reviewer-docs", 1)},
				{"review PASS from a role that already passed", "review", pass("reviewer-ux", 1)},
				{"approve result after merge requested", domain.StageMerge, result("approver", 1, domain.ApproveResult{Decision: domain.ApproveApprove})},
				{"human approval while in approve", "approve", domain.HumanApproved{Job: jobID, By: "pablogore", At: at(1)}},
				{"result in a stage the pipeline lacks", "deploy", result("intake", 1, domain.IntakeResult{Decision: domain.IntakeAccept})},
			}
			specs.Table(s, rows, func(r illegalRow) string { return r.name }, func(ctx *specs.Context, r illegalRow) {
				job := newJob()
				job.Stage = r.stage
				job.Passed = []string{"reviewer-ux"}
				before := newJob()
				before.Stage = r.stage
				before.Passed = []string{"reviewer-ux"}

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(before)
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.When("reviews come in", func(s *specs.Spec) {
			rows := []reviewRow{
				{"first PASS waits for the rest", 1, nil, nil, pass("reviewer-code", 1),
					[]domain.Event{passed("reviewer-code", 1)}},
				{"fourth PASS still waits", 1, reviewers[:3], nil, pass("security-deep", 1),
					[]domain.Event{passed("security-deep", 1)}},
				{"last PASS in any order enters approve", 1, []string{"security-deep", "reviewer-code", "reviewer-ui", "reviewer-ux"}, nil, pass("reviewer-security", 1),
					[]domain.Event{passed("reviewer-security", 1), entered("approve", 1)}},
				{"first CHANGES waits for the rest", 1, nil, nil, changes("reviewer-code", 1),
					[]domain.Event{asked("reviewer-code", 1)}},
				{"CHANGES with one reviewer left still waits", 1, reviewers[:3], nil, changes("reviewer-code", 1),
					[]domain.Event{asked("reviewer-code", 1)}},
				{"last PASS after a CHANGES in round 1 goes back to build at round 2", 1, reviewers[:3], []string{"reviewer-code"}, pass("security-deep", 1),
					[]domain.Event{passed("security-deep", 1), domain.RoundStarted{Job: jobID, Round: 2, At: at(1)}, entered("build", 1)}},
				{"last CHANGES in round 1 goes back to build at round 2", 1, reviewers[:4], nil, changes("security-deep", 1),
					[]domain.Event{asked("security-deep", 1), domain.RoundStarted{Job: jobID, Round: 2, At: at(1)}, entered("build", 1)}},
				{"CHANGES in round 2 of 3 goes back to build at round 3", 2, reviewers[1:], nil, changes("reviewer-security", 1),
					[]domain.Event{asked("reviewer-security", 1), domain.RoundStarted{Job: jobID, Round: 3, At: at(1)}, entered("build", 1)}},
				{"CHANGES in round 3 of 3 ends in needs-human naming the reviewers", 3, []string{"reviewer-ux", "reviewer-ui"}, []string{"security-deep", "reviewer-security"}, changes("reviewer-code", 1),
					[]domain.Event{asked("reviewer-code", 1), domain.NeedsHuman{Job: jobID,
						Reason: "CHANGES in the last round (3 of 3) from reviewer-security, reviewer-code, security-deep", At: at(1)}}},
				{"all PASS in round 3 of 3 enters approve", 3, reviewers[:4], nil, pass("security-deep", 1),
					[]domain.Event{passed("security-deep", 1), entered("approve", 1)}},
			}
			specs.Table(s, rows, func(r reviewRow) string { return r.name }, func(ctx *specs.Context, r reviewRow) {
				job := newJob()
				job.Stage, job.Round, job.Passed, job.Changes = "review", r.round, r.passed, r.changed

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})

			s.It("rejects a second verdict from a role that asked for CHANGES", func(ctx *specs.Context) {
				job := newJob()
				job.Stage, job.Changes = "review", []string{"reviewer-code"}

				for _, ev := range []domain.Event{pass("reviewer-code", 1), changes("reviewer-code", 1)} {
					evs, err := decide.Decide(job, ev, defaultPipeline())

					ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
					ctx.Expect(evs).To(specs.BeEmpty())
				}
			})
		})

		s.When("a reviewer asks for CHANGES in round 1", func(s *specs.Spec) {
			// toReview walks job from build through the gate into review.
			toReview := func(ctx *specs.Context, job domain.Job, minute int) domain.Job {
				for _, ev := range []domain.Event{
					result("builder", minute, domain.BuildResult{Status: domain.BuildReady}),
					domain.GatePassed{Job: jobID, At: at(minute)},
				} {
					evs, err := decide.Decide(job, ev, defaultPipeline())
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
				}
				return job
			}
			// reviewRound has every reviewer answer, reviewer-code with verdict.
			reviewRound := func(ctx *specs.Context, job domain.Job, verdict domain.ReviewVerdict) domain.Job {
				for _, role := range reviewers {
					v := domain.ReviewPass
					if role == "reviewer-code" {
						v = verdict
					}
					evs, err := decide.Decide(job, result(role, 20, domain.ReviewResult{Verdict: v}), defaultPipeline())
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
				}
				return job
			}

			s.It("starts round 2 in build with no reviewer counted as finished", func(ctx *specs.Context) {
				job := reviewRound(ctx, walk(ctx, happyPath(), 5), domain.ReviewChanges)

				ctx.Expect(job.Stage).ToEqual("build")
				ctx.Expect(job.Round).ToEqual(2)
				ctx.Expect(job.Passed).To(specs.BeEmpty())
				ctx.Expect(job.Changes).To(specs.BeEmpty())
			})

			s.It("runs every reviewer again in round 2 and enters approve when all pass", func(ctx *specs.Context) {
				job := reviewRound(ctx, walk(ctx, happyPath(), 5), domain.ReviewChanges)
				job = reviewRound(ctx, toReview(ctx, job, 30), domain.ReviewPass)

				ctx.Expect(job.Stage).ToEqual("approve")
				ctx.Expect(job.Round).ToEqual(2)
			})

			s.It("ends in needs-human after CHANGES in every round", func(ctx *specs.Context) {
				job := reviewRound(ctx, walk(ctx, happyPath(), 5), domain.ReviewChanges)
				job = reviewRound(ctx, toReview(ctx, job, 30), domain.ReviewChanges)
				job = reviewRound(ctx, toReview(ctx, job, 40), domain.ReviewChanges)

				ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(job.Round).ToEqual(3)
				ctx.Expect(job.Passed).To(specs.BeEmpty())
				ctx.Expect(job.Changes).To(specs.BeEmpty())
			})
		})

		s.When("a job waits in needs-human", func(s *specs.Spec) {
			needsHuman := func() domain.Job {
				job := newJob()
				job.Stage, job.Round = domain.StageNeedsHuman, 3
				return job
			}

			s.It("goes back to build in the same round with an allowed person's rework feedback", func(ctx *specs.Context) {
				job := needsHuman()

				evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(1)},
					entered("build", 1),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.Round).ToEqual(3)
			})

			s.It("requests the merge on a person's approval", func(ctx *specs.Context) {
				job := needsHuman()

				evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: "pablogore", At: at(1)}, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.MergeRequested{Job: jobID, At: at(1)}})
				ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageMerge)
			})

			rows := []messageRow{
				{"ignores a person who is not allowed", message(domain.AuthorPerson, false, 1)},
				{"ignores a bot even when marked allowed", message(domain.AuthorBot, true, 1)},
				{"ignores CI even when marked allowed", message(domain.AuthorCI, true, 1)},
				{"ignores a bot that is not allowed", message(domain.AuthorBot, false, 1)},
				{"ignores an author of unknown kind", message("", true, 1)},
			}
			specs.Table(s, rows, func(r messageRow) string { return r.name }, func(ctx *specs.Context, r messageRow) {
				job := needsHuman()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(needsHuman())
			})
		})

		s.It("leaves an allowed person's message outside needs-human unsupported", func(ctx *specs.Context) {
			job := newJob()
			job.Stage = "review"

			evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrUnsupported))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("returns exactly the same output for the same input", func(ctx *specs.Context) {
			job, p := newJob(), defaultPipeline()
			for _, st := range happyPath() {
				first, err1 := decide.Decide(job, st.ev, p)
				second, err2 := decide.Decide(job, st.ev, p)

				ctx.Expect(err1).To(specs.BeNil())
				ctx.Expect(err2).To(specs.BeNil())
				ctx.Expect(second).ToEqual(first)
				job = decide.Apply(job, first)
			}
		})

		s.It("stamps every output event with the input event's time", func(ctx *specs.Context) {
			job, p := newJob(), defaultPipeline()
			for i, st := range happyPath() {
				evs, err := decide.Decide(job, st.ev, p)
				ctx.Expect(err).To(specs.BeNil())
				for _, ev := range evs {
					ctx.Expect(eventTime(ev)).ToEqual(at(i + 1))
				}
				job = decide.Apply(job, evs)
			}
		})

		s.It("leaves the other outcomes of E01.4 unsupported", func(ctx *specs.Context) {
			job := newJob()
			job.Stage = "approve"

			evs, err := decide.Decide(job, result("approver", 1, domain.ApproveResult{Decision: domain.ApproveEscalate}), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrUnsupported))
			ctx.Expect(evs).To(specs.BeEmpty())
		})
	})
}

func eventTime(ev domain.Event) time.Time {
	switch e := ev.(type) {
	case domain.StageEntered:
		return e.At
	case domain.ReviewPassed:
		return e.At
	case domain.ChangesRequested:
		return e.At
	case domain.RoundStarted:
		return e.At
	case domain.NeedsHuman:
		return e.At
	case domain.MergeRequested:
		return e.At
	case domain.ReworkRequested:
		return e.At
	}
	return time.Time{}
}
