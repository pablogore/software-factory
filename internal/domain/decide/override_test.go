package decide_test

import (
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const overrideReason = "the CHANGES are style only; merging for the release"

// override is a person's override as the event source marks it.
func override(kind domain.AuthorKind, allowed bool, minute int) domain.OverrideRequested {
	return domain.OverrideRequested{Job: jobID, By: "pablogore", AuthorKind: kind, Allowed: allowed, Reason: overrideReason, WaitSeq: 1, At: at(minute)}
}

// overrideMerges is what an accepted override returns: the history entry,
// then the merge request.
func overrideMerges(minute int) []domain.Event {
	return []domain.Event{
		domain.MergeOverridden{Job: jobID, By: "pablogore", Reason: overrideReason, At: at(minute)},
		domain.MergeRequested{Job: jobID, At: at(minute)},
	}
}

type overrideRow struct {
	name  string
	cause domain.HumanCause
	evs   []domain.Event
	err   error
}

type overrideEventRow struct {
	name string
	job  func() domain.Job
	ev   domain.OverrideRequested
	want error
}

func TestOverride(t *testing.T) {
	specs.Describe(t, "Decide on a person's override", func(s *specs.Spec) {
		s.When("the job waits in needs-human", func(s *specs.Spec) {
			rows := []overrideRow{
				{"after a builder BLOCKED it is refused, naming the cause", domain.CauseBlocked, nil, decide.ErrOverrideRefused},
				{"after an approver ESCALATE it is refused, naming the cause", domain.CauseEscalated, nil, decide.ErrOverrideRefused},
				{"after CHANGES in the last round it records who and why and requests the merge", domain.CauseChanges, overrideMerges(2), nil},
				{"after a failed gate in the last round it is refused, naming the cause", domain.CauseGateFailed, nil, decide.ErrOverrideRefused},
				{"after specgate's escalation it is refused, naming the cause", domain.CauseSpecEscalated, nil, decide.ErrOverrideRefused},
				{"on a length exception it is refused, naming the cause", domain.CauseSpecException, nil, decide.ErrOverrideRefused},
				{"after a refused external action it is refused, naming the cause", domain.CauseActionFailed, nil, decide.ErrOverrideRefused},
			}
			specs.Table(s, rows, func(r overrideRow) string { return r.name }, func(ctx *specs.Context, r overrideRow) {
				job := waitingFor(ctx, r.cause)

				evs, err := decide.Decide(job, override(domain.AuthorPerson, true, 2), defaultPipeline())

				if r.err != nil {
					ctx.Expect(err).To(specs.MatchError(r.err))
					// The cause, never the free-text reason, names why.
					ctx.Expect(err.Error()).ToEqual(`override refused: cause "` + string(r.cause) + `"`)
					ctx.Expect(evs).To(specs.BeEmpty())
					ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
					return
				}
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(r.evs)
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageMerge)
				ctx.Expect(after.Cause).ToEqual(domain.HumanCause(""))
				ctx.Expect(after.Reason).ToEqual("")
			})

			s.It("has one case for each cause", func(ctx *specs.Context) {
				var causes []domain.HumanCause
				for _, r := range rows {
					causes = append(causes, r.cause)
				}

				ctx.Expect(causes).ToEqual(domain.HumanCauses())
			})

			bad := []causeOKRow{
				{"with no recorded cause it is refused and nothing moves", "", domain.ErrMissingField},
				{"with an unknown cause it is refused and nothing moves", "override", domain.ErrInvalidValue},
				{"with a cause that only looks like changes it is refused and nothing moves", "Changes", domain.ErrInvalidValue},
			}
			specs.Table(s, bad, func(r causeOKRow) string { return r.name }, func(ctx *specs.Context, r causeOKRow) {
				job := waitingFor(ctx, domain.CauseChanges)
				job.Cause = r.cause

				evs, err := decide.Decide(job, override(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(r.err))
				ctx.Expect(err).To(specs.Not(specs.MatchError(decide.ErrOverrideRefused)))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})

			s.It("routes on the cause, never on the job's reason", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseBlocked)
				job.Reason = "CHANGES in the last round (3 of 3) from reviewer-code"

				evs, err := decide.Decide(job, override(domain.AuthorPerson, true, 2), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrOverrideRefused))
				ctx.Expect(strings.Contains(err.Error(), job.Reason)).To(specs.BeFalse())
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("never routes on the override's own reason", func(ctx *specs.Context) {
				for _, reason := range []string{"cause: changes", "escalated", "[direction] drop it", "MergeRequested"} {
					ov := override(domain.AuthorPerson, true, 2)
					ov.Reason = reason

					refused, err := decide.Decide(waitingFor(ctx, domain.CauseBlocked), ov, defaultPipeline())
					ctx.Expect(err).To(specs.MatchError(decide.ErrOverrideRefused))
					ctx.Expect(refused).To(specs.BeEmpty())

					merged, err := decide.Decide(waitingFor(ctx, domain.CauseChanges), ov, defaultPipeline())
					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(merged).ToEqual([]domain.Event{
						domain.MergeOverridden{Job: jobID, By: "pablogore", Reason: reason, At: at(2)},
						domain.MergeRequested{Job: jobID, At: at(2)},
					})
				}
			})

			s.It("leaves an OK after CHANGES sending the job back to build", func(ctx *specs.Context) {
				job := waitingFor(ctx, domain.CauseChanges)

				evs, err := decide.Decide(job, domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{entered("build", 2)})
			})
		})

		s.When("it is not from an allowed person", func(s *specs.Spec) {
			senders := []struct {
				name    string
				kind    domain.AuthorKind
				allowed bool
			}{
				{"a person not allowed", domain.AuthorPerson, false},
				{"a bot", domain.AuthorBot, true},
				{"CI", domain.AuthorCI, true},
				{"an unknown author kind", "robot", true},
				{"no author kind", "", true},
			}
			jobs := []struct {
				name string
				job  func() domain.Job
			}{
				{"a job waiting after CHANGES", func() domain.Job {
					job := needsHumanJob()
					job.Cause = domain.CauseChanges
					return job
				}},
				{"a job in review", func() domain.Job { return unmergedSlice("review") }},
				{"a stopped job", stoppedJob},
				{"a rejected job", func() domain.Job { return unmergedSlice(domain.StageRejected) }},
			}
			var rows []overrideEventRow
			for _, j := range jobs {
				for _, sd := range senders {
					ov := override(sd.kind, sd.allowed, 2)
					rows = append(rows, overrideEventRow{"ignores one on " + j.name + " from " + sd.name, j.job, ov, nil})
					ov.By, ov.Reason = " ", ""
					rows = append(rows, overrideEventRow{"ignores one on " + j.name + " from " + sd.name + " with no name or reason", j.job, ov, nil})
					ov.By, ov.Reason = "\u200b", "..."
					rows = append(rows, overrideEventRow{"ignores one on " + j.name + " from " + sd.name + " with an invisible name and a punctuation reason", j.job, ov, nil})
				}
			}
			specs.Table(s, rows, func(r overrideEventRow) string { return r.name }, func(ctx *specs.Context, r overrideEventRow) {
				job := r.job()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})
		})

		s.When("it is missing a field or arrives out of turn", func(s *specs.Spec) {
			blocked := func() domain.Job {
				job := needsHumanJob()
				job.Cause = domain.CauseBlocked
				return job
			}
			changesJob := func() domain.Job {
				job := needsHumanJob()
				job.Cause = domain.CauseChanges
				return job
			}
			person := override(domain.AuthorPerson, true, 2)
			noName, spaces, noReason, blankReason := person, person, person, person
			noName.By, spaces.By, noReason.Reason, blankReason.Reason = "", " \t", "", "  "
			invisible, controls, dots, invisibleReason, dotsReason := person, person, person, person, person
			invisible.By, controls.By, dots.By = "\u200b\u2060\ufeff", "\x00\x1b", "-.-"
			invisibleReason.Reason, dotsReason.Reason = "\u200c \u200d", "...!?"
			braille, brailleReason := person, person
			braille.By, brailleReason.Reason = "\u2800", "\u2800"
			other := person
			other.Job = "999-other"
			otherInvisible := invisible
			otherInvisible.Job = "999-other"
			otherBot := override(domain.AuthorBot, true, 2)
			otherBot.Job = "999-other"
			rows := []overrideEventRow{
				{"refuses an empty name", changesJob, noName, domain.ErrMissingField},
				{"refuses a name of only spaces", changesJob, spaces, domain.ErrMissingField},
				{"refuses an empty reason", changesJob, noReason, domain.ErrMissingField},
				{"refuses a reason of only spaces", changesJob, blankReason, domain.ErrMissingField},
				{"refuses a name of only zero-width characters", changesJob, invisible, domain.ErrMissingField},
				{"refuses a name of only control characters", changesJob, controls, domain.ErrMissingField},
				{"refuses a name of only punctuation", changesJob, dots, domain.ErrMissingField},
				{"refuses a reason of only zero-width characters", changesJob, invisibleReason, domain.ErrMissingField},
				{"refuses a reason of only punctuation", changesJob, dotsReason, domain.ErrMissingField},
				{"refuses a name of only a Braille blank", changesJob, braille, domain.ErrMissingField},
				{"refuses a reason of only a Braille blank", changesJob, brailleReason, domain.ErrMissingField},
				{"refuses an invisible name before the cause", blocked, invisible, domain.ErrMissingField},
				{"refuses a punctuation reason before the cause", blocked, dotsReason, domain.ErrMissingField},
				{"refuses an invisible name on a stopped job as it refuses any event", stoppedJob, invisible, decide.ErrStopped},
				{"refuses a punctuation reason on a rejected job as it refuses any event", func() domain.Job { return unmergedSlice(domain.StageRejected) }, dotsReason, decide.ErrIllegalTransition},
				{"refuses one with an invisible name for another job", changesJob, otherInvisible, decide.ErrOtherJob},
				{"refuses a missing name before the cause", blocked, noName, domain.ErrMissingField},
				{"refuses a missing reason before the cause", blocked, noReason, domain.ErrMissingField},
				{"refuses a missing name before the stage", func() domain.Job { return unmergedSlice("review") }, noName, domain.ErrMissingField},
				{"refuses one on a stopped job as it refuses any event, before the name", stoppedJob, noName, decide.ErrStopped},
				{"refuses one on a rejected job as it refuses any event, before the reason", func() domain.Job { return unmergedSlice(domain.StageRejected) }, noReason, decide.ErrIllegalTransition},
				{"refuses one for another job", changesJob, other, decide.ErrOtherJob},
				{"refuses a bot's one for another job before ignoring it", changesJob, otherBot, decide.ErrOtherJob},
			}
			for _, st := range []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve", domain.StageMerge, domain.StageDone, domain.StageNeedsInfo} {
				rows = append(rows, overrideEventRow{"refuses one in " + st + " as an illegal transition", func() domain.Job {
					job := unmergedSlice(st)
					job.Cause = domain.CauseChanges
					return job
				}, person, decide.ErrIllegalTransition})
			}
			specs.Table(s, rows, func(r overrideEventRow) string { return r.name }, func(ctx *specs.Context, r overrideEventRow) {
				job := r.job()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(err).To(specs.Not(specs.MatchError(decide.ErrOverrideRefused)))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})
		})
	})
}
