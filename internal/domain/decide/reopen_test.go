package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

func merged(minute int) domain.MergeCompleted {
	return domain.MergeCompleted{Job: jobID, At: at(minute)}
}

// doneJob is a job merged after its last review round.
func doneJob() domain.Job {
	job := newJob()
	job.Stage, job.Round, job.WaitSeq = domain.StageDone, 3, 1
	return job
}

func onThread(provider, thread string) domain.MessageReceived {
	m := message(domain.AuthorPerson, true, 1)
	m.Provider, m.Thread = provider, thread
	return m
}

func TestReopen(t *testing.T) {
	specs.Describe(t, "Decide on a finished job", func(s *specs.Spec) {
		s.When("the merge is reported as completed", func(s *specs.Spec) {
			s.It("finishes a job that asked to merge", func(ctx *specs.Context) {
				job := newJob()
				job.Stage = domain.StageMerge

				evs, err := decide.Decide(job, merged(1), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{entered(domain.StageDone, 1)})
				ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageDone)
			})

			s.It("leaves a job walking the happy path in merge until the merge completes", func(ctx *specs.Context) {
				steps := happyPath()
				job := walk(ctx, steps, len(steps))
				ctx.Expect(job.Stage).ToEqual(domain.StageMerge)

				evs, err := decide.Decide(job, merged(12), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(decide.Apply(job, evs).Stage).ToEqual(domain.StageDone)
			})

			stages := []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve", domain.StageNeedsHuman, domain.StageDone}
			specs.Table(s, stages, func(st string) string { return "in " + st + " is refused" }, func(ctx *specs.Context, st string) {
				job, before := newJob(), newJob()
				job.Stage, before.Stage = st, st

				evs, err := decide.Decide(job, merged(1), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(before)
			})
		})

		s.When("an allowed person comments on a finished job", func(s *specs.Spec) {
			rows := []messageRow{
				{"reopens it in build from the PR", onThread("github", "pr/88")},
				{"reopens it in build from the issue", onThread("github", "issue/27")},
				{"reopens it in build from a chat thread", onThread("slack", "C042/1730800000.000100")},
			}
			specs.Table(s, rows, func(r messageRow) string { return r.name }, func(ctx *specs.Context, r messageRow) {
				job := doneJob()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ReworkRequested{Job: jobID, By: "pablogore", Text: "rename the flag", At: at(1)},
					entered("build", 1),
				})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.Round).ToEqual(3)
			})

			s.It("sends a reopened job in its last round to needs-human on CHANGES", func(ctx *specs.Context) {
				job := doneJob()
				for _, ev := range []domain.Event{
					message(domain.AuthorPerson, true, 1),
					result("builder", 2, domain.BuildResult{Status: domain.BuildReady}),
					domain.GatePassed{Job: jobID, At: at(3)},
					pass("reviewer-security", 4), pass("reviewer-ux", 4), pass("reviewer-ui", 4), pass("security-deep", 4),
					changes("reviewer-code", 5),
				} {
					evs, err := decide.Decide(job, ev, defaultPipeline())
					ctx.Expect(err).To(specs.BeNil())
					job = decide.Apply(job, evs)
				}

				ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(job.Round).ToEqual(3)
			})
		})

		s.When("someone else comments on a finished job", func(s *specs.Spec) {
			rows := []messageRow{
				{"ignores a person who is not allowed", message(domain.AuthorPerson, false, 1)},
				{"ignores a bot even when marked allowed", message(domain.AuthorBot, true, 1)},
				{"ignores CI even when marked allowed", message(domain.AuthorCI, true, 1)},
				{"ignores an author of unknown kind", message("", true, 1)},
			}
			specs.Table(s, rows, func(r messageRow) string { return r.name }, func(ctx *specs.Context, r messageRow) {
				job := doneJob()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job).ToEqual(doneJob())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(doneJob())
			})
		})

		s.It("leaves an allowed person's message to a job asking to merge unsupported", func(ctx *specs.Context) {
			job := newJob()
			job.Stage, job.WaitSeq = domain.StageMerge, 1

			evs, err := decide.Decide(job, message(domain.AuthorPerson, true, 1), defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrUnsupported))
			ctx.Expect(evs).To(specs.BeEmpty())
		})

		s.It("refuses a person's approval on a finished job", func(ctx *specs.Context) {
			evs, err := decide.Decide(doneJob(), domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(1)}, defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			ctx.Expect(evs).To(specs.BeEmpty())
		})
	})
}

// nilEvents are a missing event, a nil pointer of every event kind Decide
// handles and a result event whose result is a nil pointer.
func nilEvents() []eventRow {
	return []eventRow{
		{"no event", nil},
		{"a nil result event", (*domain.ResultSubmitted)(nil)},
		{"a nil message", (*domain.MessageReceived)(nil)},
		{"a nil stop request", (*domain.StopRequested)(nil)},
		{"a nil approval", (*domain.HumanApproved)(nil)},
		{"a nil override", (*domain.OverrideRequested)(nil)},
		{"a nil merge completed", (*domain.MergeCompleted)(nil)},
		{"a nil gate OK", (*domain.GatePassed)(nil)},
		{"a nil failed action", (*domain.ActionFailed)(nil)},
		{"a result event with a nil result", result("intake", 1, (*domain.IntakeResult)(nil))},
	}
}

func TestNilEvent(t *testing.T) {
	specs.Describe(t, "Decide on a missing event", func(s *specs.Spec) {
		stages := []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve",
			domain.StageMerge, domain.StageDone, domain.StageNeedsHuman, domain.StageNeedsInfo, domain.StageStopped, domain.StageRejected}
		type nilRow struct {
			name  string
			stage string
			ev    domain.Event
			want  error
		}
		var rows []nilRow
		for _, st := range stages {
			for _, r := range nilEvents() {
				// A stopped job refuses a real result event before looking at its result.
				want := decide.ErrIllegalTransition
				if _, ok := r.ev.(domain.ResultSubmitted); ok && st == domain.StageStopped {
					want = decide.ErrStopped
				}
				rows = append(rows, nilRow{"rejects " + r.name + " in " + st, st, r.ev, want})
			}
		}
		specs.Table(s, rows, func(r nilRow) string { return r.name }, func(ctx *specs.Context, r nilRow) {
			job, before := newJob(), newJob()
			job.Stage, before.Stage = r.stage, r.stage

			evs, err := decide.Decide(job, r.ev, defaultPipeline())

			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(evs).To(specs.BeEmpty())
			ctx.Expect(job).ToEqual(before)
		})
	})
}
