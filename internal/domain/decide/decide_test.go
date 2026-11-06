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
			{Name: "review", Kind: domain.KindReview, Roles: reviewers, OnChanges: "build", MaxRounds: new(3)},
			{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
		},
	}
}

// modesPipeline is defaultPipeline with every reviewer's mode declared:
// security-deep is new in shadow, reviewer-ux in advisory, reviewer-code has
// no mode and the rest block.
func modesPipeline() domain.Pipeline {
	p := defaultPipeline()
	p.Roles = []domain.Role{
		{Name: "reviewer-security", Kind: domain.KindReview, Mode: domain.ModeBlocking},
		{Name: "reviewer-ux", Kind: domain.KindReview, Mode: domain.ModeAdvisory},
		{Name: "reviewer-ui", Kind: domain.KindReview, Mode: domain.ModeBlocking},
		{Name: "reviewer-code", Kind: domain.KindReview},
		{Name: "security-deep", Kind: domain.KindReview, Mode: domain.ModeShadow},
	}
	return p
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

func started(round, minute int, roles ...string) domain.RoundStarted {
	return domain.RoundStarted{Job: jobID, Round: round, Reviewers: roles, At: at(minute)}
}

func entered(stage string, minute int) domain.StageEntered {
	return domain.StageEntered{Job: jobID, Stage: stage, At: at(minute)}
}

func passed(role string, minute int) domain.ReviewPassed {
	return domain.ReviewPassed{Job: jobID, Role: role, At: at(minute)}
}

func message(kind domain.AuthorKind, allowed bool, minute int) domain.MessageReceived {
	return domain.MessageReceived{Job: jobID, Provider: "github", Thread: "pr/88", Author: "pablogore",
		AuthorKind: kind, Allowed: allowed, Text: "rename the flag", WaitSeq: 1, At: at(minute)}
}

// answering is m sent for the job's wait seq instead of its first.
func answering(m domain.MessageReceived, seq int) domain.MessageReceived {
	m.WaitSeq = seq
	return m
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

type escalateRow struct {
	name   string
	round  int
	ev     domain.ResultSubmitted
	reason string
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
				{"human approval while in approve", "approve", domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(1)}},
				{"result in a stage the pipeline lacks", "deploy", result("intake", 1, domain.IntakeResult{Decision: domain.IntakeAccept})},
			}
			specs.Table(s, rows, func(r illegalRow) string { return r.name }, func(ctx *specs.Context, r illegalRow) {
				// One wait behind it, so a person's action is fresh and only the
				// stage refuses it.
				job := newJob()
				job.Stage, job.WaitSeq = r.stage, 1
				job.Passed = []string{"reviewer-ux"}
				before := newJob()
				before.Stage, before.WaitSeq = r.stage, 1
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
					[]domain.Event{passed("security-deep", 1), started(2, 1, "reviewer-code"), entered("build", 1)}},
				{"last CHANGES in round 1 goes back to build at round 2", 1, reviewers[:4], nil, changes("security-deep", 1),
					[]domain.Event{asked("security-deep", 1), started(2, 1, "security-deep"), entered("build", 1)}},
				{"CHANGES in round 2 of 3 goes back to build at round 3", 2, reviewers[1:], nil, changes("reviewer-security", 1),
					[]domain.Event{asked("reviewer-security", 1), started(3, 1, "reviewer-security"), entered("build", 1)}},
				{"CHANGES in round 3 of 3 ends in needs-human naming the reviewers", 3, []string{"reviewer-ux", "reviewer-ui"}, []string{"security-deep", "reviewer-security"}, changes("reviewer-code", 1),
					[]domain.Event{asked("reviewer-code", 1), domain.NeedsHuman{Job: jobID,
						Reason: "CHANGES in the last round (3 of 3) from reviewer-security, reviewer-code, security-deep", Cause: domain.CauseChanges, At: at(1)}}},
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

		s.When("reviewers in shadow or advisory mode ask for CHANGES", func(s *specs.Spec) {
			roundTwo := func(role string, rerun ...string) []domain.Event {
				return []domain.Event{asked(role, 1), started(2, 1, rerun...), entered("build", 1)}
			}
			rows := []reviewRow{
				{"shadow CHANGES alone enters approve", 1, reviewers[:4], nil, changes("security-deep", 1),
					[]domain.Event{asked("security-deep", 1), entered("approve", 1)}},
				{"advisory CHANGES alone enters approve", 1, []string{"reviewer-security", "reviewer-ui", "reviewer-code", "security-deep"}, nil, changes("reviewer-ux", 1),
					[]domain.Event{asked("reviewer-ux", 1), entered("approve", 1)}},
				{"last PASS after shadow and advisory CHANGES enters approve", 1, []string{"reviewer-security", "reviewer-ui", "reviewer-code"}, []string{"reviewer-ux"}, changes("security-deep", 1),
					[]domain.Event{asked("security-deep", 1), entered("approve", 1)}},
				{"shadow and advisory CHANGES in round 3 of 3 enter approve, not needs-human", 3, []string{"reviewer-security", "reviewer-ui", "reviewer-code"}, []string{"security-deep"}, changes("reviewer-ux", 3),
					[]domain.Event{asked("reviewer-ux", 3), entered("approve", 3)}},
				{"mixed round with a blocking CHANGES goes back to build at round 2", 1, []string{"reviewer-ui", "reviewer-code"}, []string{"reviewer-ux", "security-deep"}, changes("reviewer-security", 1),
					roundTwo("reviewer-security", "reviewer-security", "reviewer-ux", "security-deep")},
				{"mixed round in round 3 of 3 ends in needs-human naming only the blocking reviewer", 3, []string{"reviewer-ui", "reviewer-code"}, []string{"reviewer-ux", "security-deep"}, changes("reviewer-security", 1),
					[]domain.Event{asked("reviewer-security", 1), domain.NeedsHuman{Job: jobID,
						Reason: "CHANGES in the last round (3 of 3) from reviewer-security", Cause: domain.CauseChanges, At: at(1)}}},
				{"CHANGES from a role with no mode goes back to build at round 2", 1, []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "security-deep"}, nil, changes("reviewer-code", 1),
					roundTwo("reviewer-code", "reviewer-code")},
				{"CHANGES from a role with no mode in round 3 of 3 ends in needs-human", 3, []string{"reviewer-security", "reviewer-ux", "reviewer-ui", "security-deep"}, nil, changes("reviewer-code", 1),
					[]domain.Event{asked("reviewer-code", 1), domain.NeedsHuman{Job: jobID,
						Reason: "CHANGES in the last round (3 of 3) from reviewer-code", Cause: domain.CauseChanges, At: at(1)}}},
			}
			specs.Table(s, rows, func(r reviewRow) string { return r.name }, func(ctx *specs.Context, r reviewRow) {
				job := newJob()
				job.Stage, job.Round, job.Passed, job.Changes = "review", r.round, r.passed, r.changed

				evs, err := decide.Decide(job, r.ev, modesPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.want)
			})

			s.It("walks a round where only shadow and advisory reviewers ask for CHANGES into approve", func(ctx *specs.Context) {
				job := walk(ctx, happyPath(), 5)
				for _, ev := range []domain.Event{changes("security-deep", 20), changes("reviewer-ux", 21),
					pass("reviewer-security", 22), pass("reviewer-ui", 23), pass("reviewer-code", 24)} {
					evs, err := decide.Decide(job, ev, modesPipeline())
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
				}

				ctx.Expect(job.Stage).ToEqual("approve")
				ctx.Expect(job.Round).ToEqual(1)
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
			// reviewRound has every reviewer of the round answer, reviewer-code
			// with verdict.
			reviewRound := func(ctx *specs.Context, job domain.Job, verdict domain.ReviewVerdict) domain.Job {
				round := reviewers
				if len(job.Reviewers) > 0 {
					round = job.Reviewers
				}
				for _, role := range round {
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

			s.It("starts round 2 in build with only reviewer-code to run again", func(ctx *specs.Context) {
				job := reviewRound(ctx, walk(ctx, happyPath(), 5), domain.ReviewChanges)

				ctx.Expect(job.Stage).ToEqual("build")
				ctx.Expect(job.Round).ToEqual(2)
				ctx.Expect(job.Reviewers).ToEqual([]string{"reviewer-code"})
				ctx.Expect(job.Passed).To(specs.BeEmpty())
				ctx.Expect(job.Changes).To(specs.BeEmpty())
			})

			s.It("runs only reviewer-code in round 2 and enters approve when it passes", func(ctx *specs.Context) {
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
				ctx.Expect(job.Reviewers).To(specs.BeEmpty())
				ctx.Expect(job.Passed).To(specs.BeEmpty())
				ctx.Expect(job.Changes).To(specs.BeEmpty())
			})
		})

		s.When("a job waits in needs-human", func(s *specs.Spec) {
			needsHuman := func() domain.Job {
				job := newJob()
				job.Stage, job.Round, job.Cause, job.WaitSeq = domain.StageNeedsHuman, 3, domain.CauseChanges, 1
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

			s.It("requests the merge on a person's approval after an approver ESCALATE", func(ctx *specs.Context) {
				job := needsHuman()
				job.Cause = domain.CauseEscalated

				evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(1)}, defaultPipeline())

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

		s.When("the approver escalates", func(s *specs.Spec) {
			escalate := func(reasons ...string) domain.ResultSubmitted {
				return result("approver", 1, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: reasons})
			}
			rows := []escalateRow{
				{"ESCALATE with two reasons in round 1 ends in needs-human with both", 1,
					escalate("touches the billing schema", "no rollback plan"),
					"approver escalated: touches the billing schema; no rollback plan"},
				{"ESCALATE with one reason in round 2 ends in needs-human", 2,
					escalate("the spec and the PR disagree"),
					"approver escalated: the spec and the PR disagree"},
				{"ESCALATE in the last round ends in needs-human", 3,
					escalate("needs a product decision"),
					"approver escalated: needs a product decision"},
				{"ESCALATE with no reasons still ends in needs-human", 1, escalate(), "approver escalated"},
			}
			specs.Table(s, rows, func(r escalateRow) string { return r.name }, func(ctx *specs.Context, r escalateRow) {
				job := newJob()
				job.Stage, job.Round = "approve", r.round

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: r.reason, Cause: domain.CauseEscalated, At: at(1)}})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Round).ToEqual(r.round)
			})

			s.It("stops the happy path before merge", func(ctx *specs.Context) {
				steps := happyPath()
				job := walk(ctx, steps, len(steps)-1)

				evs, err := decide.Decide(job, result("approver", 11, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"too risky"}}), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.NeedsHuman{Job: jobID, Reason: "approver escalated: too risky", Cause: domain.CauseEscalated, At: at(11)}})
				ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageNeedsHuman)
			})
		})

		s.It("leaves an allowed person's message outside needs-human unsupported", func(ctx *specs.Context) {
			job := newJob()
			job.Stage, job.WaitSeq = "review", 1

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
	case domain.NeedsInfo:
		return e.At
	case domain.InfoAnswered:
		return e.At
	}
	return time.Time{}
}
