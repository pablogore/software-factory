package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// limitOverride is a person's override of one limit for the job's wait seq,
// as the event source marks it.
func limitOverride(kind domain.AuthorKind, allowed bool, limit, value string, seq, minute int) domain.LimitOverrideRequested {
	return domain.LimitOverrideRequested{Job: jobID, By: "pablogore", AuthorKind: kind, Allowed: allowed, Limit: limit, Value: value, WaitSeq: seq, At: at(minute)}
}

// signedLimit is o with its requester replaced by by.
func signedLimit(o domain.LimitOverrideRequested, by string) domain.LimitOverrideRequested {
	o.By = by
	return o
}

// raised is a person's allowed override of limit to value on job's current wait.
func raised(job domain.Job, limit, value string, minute int) domain.LimitOverrideRequested {
	return limitOverride(domain.AuthorPerson, true, limit, value, job.WaitSeq, minute)
}

// overrideOnce decides an allowed person's override on job and applies it.
func overrideOnce(ctx *specs.Context, job domain.Job, limit, value string, p domain.Pipeline) domain.Job {
	evs, err := decide.Decide(job, raised(job, limit, value, 12), p)
	ctx.Expect(err).To(specs.BeNil())
	return decide.Apply(job, evs)
}

type limitSetRow struct {
	name  string
	limit string
	value string
	want  domain.LimitOverridden
}

type badLimitRow struct {
	name  string
	limit string
	value string
	err   error
	text  string
}

type limitEventRow struct {
	name string
	job  func() domain.Job
	ev   func(domain.Job) domain.LimitOverrideRequested
	want error
}

func TestLimitOverride(t *testing.T) {
	specs.Describe(t, "Decide on a person's limit override", func(s *specs.Spec) {
		s.When("the job waits in needs-human", func(s *specs.Spec) {
			s.It("raises human_reworks at the cap so the next rework is accepted", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := usedUp(ctx, p)

				evs, err := decide.Decide(job, raised(job, "human_reworks", "3", 12), p)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.LimitOverridden{Job: jobID, By: "pablogore", Limit: "human_reworks", Value: 3, At: at(12)}})
				after := decide.Apply(job, evs)
				ctx.Expect(after.Stage).ToEqual(domain.StageNeedsHuman)
				ctx.Expect(after.Cause).ToEqual(job.Cause)
				ctx.Expect(after.Reason).ToEqual(job.Reason)
				ctx.Expect(after.WaitSeq).ToEqual(job.WaitSeq + 1)

				reworked := reworkOnce(ctx, after, p)
				ctx.Expect(reworked.Stage).ToEqual("build")
				ctx.Expect(reworked.HumanReworks).ToEqual(3)
			})

			s.It("refuses the rework past the new value, naming it", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := overrideOnce(ctx, usedUp(ctx, p), "human_reworks", "3", p)
				job = escalateAgain(ctx, reworkOnce(ctx, job, p), p)

				evs, err := decide.Decide(job, feedbackOn(job, 12), p)

				ctx.Expect(err).To(specs.MatchError(decide.ErrReworkRefused))
				ctx.Expect(err.Error()).ToEqual("rework refused: 3 of human_reworks 3 used")
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("leaves every other job on the pipeline's value", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				overrideOnce(ctx, usedUp(ctx, p), "human_reworks", "3", p)
				other := usedUp(ctx, p)

				evs, err := decide.Decide(other, feedbackOn(other, 12), p)

				ctx.Expect(err).To(specs.MatchError(decide.ErrReworkRefused))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(p.Limits.HumanReworks).ToEqual(new(2))
			})

			s.It("lifts human_reworks with unlimited", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := overrideOnce(ctx, usedUp(ctx, p), "human_reworks", domain.Unlimited, p)
				job.HumanReworks = 50

				after := reworkOnce(ctx, job, p)

				ctx.Expect(after.Stage).ToEqual("build")
				ctx.Expect(after.HumanReworks).ToEqual(51)
			})

			s.It("caps human_reworks again when a number follows unlimited", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := overrideOnce(ctx, usedUp(ctx, p), "human_reworks", domain.Unlimited, p)
				job = overrideOnce(ctx, job, "human_reworks", "2", p)

				evs, err := decide.Decide(job, feedbackOn(job, 13), p)

				ctx.Expect(job.ReworksLifted).To(specs.BeFalse())
				ctx.Expect(err).To(specs.MatchError(decide.ErrReworkRefused))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("raises max_rounds over the stage's own value for this job only", func(ctx *specs.Context) {
				job := overrideOnce(ctx, waitingFor(ctx, domain.CauseChanges), "max_rounds", "4", defaultPipeline())
				review := codeChangesIn(3)
				review.Limits = job.Limits

				evs, err := decide.Decide(review, changes("reviewer-code", 1), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(anotherRound(3))

				evs, err = decide.Decide(codeChangesIn(3), changes("reviewer-code", 1), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(lastRound(3, 3))
			})

			s.It("raises spec_rejections for this job only", func(ctx *specs.Context) {
				job := overrideOnce(ctx, waitingFor(ctx, domain.CauseSpecEscalated), "spec_rejections", "4", defaultPipeline())
				gated := atSpecgate(2)
				gated.Limits = job.Limits

				evs, err := decide.Decide(gated, specgateFailed(1), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{rejected(3, 1), entered("spec", 1)})

				evs, err = decide.Decide(atSpecgate(2), specgateFailed(1), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(decide.Apply(atSpecgate(2), evs).Stage).ToEqual(domain.StageNeedsHuman)
			})

			rows := []limitSetRow{
				{"records who set spec_rejections and to what", "spec_rejections", "5", domain.LimitOverridden{Job: jobID, By: "pablogore", Limit: "spec_rejections", Value: 5, At: at(2)}},
				{"records who set human_reworks and to what", "human_reworks", "4", domain.LimitOverridden{Job: jobID, By: "pablogore", Limit: "human_reworks", Value: 4, At: at(2)}},
				{"records who lifted human_reworks", "human_reworks", "unlimited", domain.LimitOverridden{Job: jobID, By: "pablogore", Limit: "human_reworks", Unlimited: true, At: at(2)}},
				{"records who set triage_retries and to what", "triage_retries", "2", domain.LimitOverridden{Job: jobID, By: "pablogore", Limit: "triage_retries", Value: 2, At: at(2)}},
				{"records who set max_rounds and to what", "max_rounds", "1", domain.LimitOverridden{Job: jobID, By: "pablogore", Limit: "max_rounds", Value: 1, At: at(2)}},
			}
			specs.Table(s, rows, func(r limitSetRow) string { return r.name }, func(ctx *specs.Context, r limitSetRow) {
				job := waitingFor(ctx, domain.CauseBlocked)

				evs, err := decide.Decide(job, raised(job, r.limit, r.value, 2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{r.want})
			})

			bad := []badLimitRow{
				{"refuses an unknown limit, naming it", "retries", "2", domain.ErrInvalidValue, `decide: limit override: invalid value: unknown key "retries"`},
				{"refuses a limit that only looks like one", "Max_Rounds", "2", domain.ErrInvalidValue, `decide: limit override: invalid value: unknown key "Max_Rounds"`},
				{"refuses a missing limit name", "", "2", domain.ErrInvalidValue, `decide: limit override: invalid value: unknown key ""`},
				{"refuses 0, naming the limit", "max_rounds", "0", domain.ErrInvalidValue, `decide: limit override: invalid value: max_rounds "0", want a whole number of 1 or more`},
				{"refuses a negative value, naming the limit", "spec_rejections", "-1", domain.ErrInvalidValue, `decide: limit override: invalid value: spec_rejections "-1", want a whole number of 1 or more`},
				{"refuses a value that is not a whole number", "human_reworks", "2.5", domain.ErrInvalidValue, `decide: limit override: invalid value: human_reworks "2.5", want a whole number of 1 or more or unlimited`},
				{"refuses an empty value", "human_reworks", "", domain.ErrInvalidValue, `decide: limit override: invalid value: human_reworks "", want a whole number of 1 or more or unlimited`},
				{"refuses unlimited on a limit other than human_reworks", "max_rounds", "unlimited", domain.ErrInvalidValue, `decide: limit override: invalid value: max_rounds "unlimited", want a whole number of 1 or more`},
				{"refuses unlimited written another way", "human_reworks", "Unlimited", domain.ErrInvalidValue, `decide: limit override: invalid value: human_reworks "Unlimited", want a whole number of 1 or more or unlimited`},
			}
			specs.Table(s, bad, func(r badLimitRow) string { return r.name }, func(ctx *specs.Context, r badLimitRow) {
				p := reworksPipeline(2)
				job := usedUp(ctx, p)

				evs, err := decide.Decide(job, raised(job, r.limit, r.value, 12), p)

				ctx.Expect(err).To(specs.MatchError(r.err))
				ctx.Expect(err.Error()).ToEqual(r.text)
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
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
				{"a job waiting in needs-human", needsHumanJob},
				{"a job in review", func() domain.Job { return unmergedSlice("review") }},
				{"a stopped job", stoppedJob},
				{"a rejected job", func() domain.Job { return unmergedSlice(domain.StageRejected) }},
			}
			var rows []limitEventRow
			for _, j := range jobs {
				for _, sd := range senders {
					rows = append(rows, limitEventRow{"ignores one on " + j.name + " from " + sd.name, j.job, func(job domain.Job) domain.LimitOverrideRequested {
						return limitOverride(sd.kind, sd.allowed, "human_reworks", "9", job.WaitSeq, 2)
					}, nil})
					rows = append(rows, limitEventRow{"ignores one on " + j.name + " from " + sd.name + " with no name or value", j.job, func(job domain.Job) domain.LimitOverrideRequested {
						ov := limitOverride(sd.kind, sd.allowed, "", "", job.WaitSeq, 2)
						ov.By = ""
						return ov
					}, nil})
					rows = append(rows, limitEventRow{"ignores one on " + j.name + " from " + sd.name + " with an invisible name", j.job, func(job domain.Job) domain.LimitOverrideRequested {
						return signedLimit(limitOverride(sd.kind, sd.allowed, "human_reworks", "9", job.WaitSeq, 2), "\u200b")
					}, nil})
				}
			}
			specs.Table(s, rows, func(r limitEventRow) string { return r.name }, func(ctx *specs.Context, r limitEventRow) {
				job := r.job()

				evs, err := decide.Decide(job, r.ev(job), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})
		})

		s.When("it is missing who asked or does not answer the current wait", func(s *specs.Spec) {
			current := func(job domain.Job) domain.LimitOverrideRequested { return raised(job, "human_reworks", "9", 2) }
			rows := []limitEventRow{
				{"refuses one with no requester", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					ov := current(job)
					ov.By = ""
					return ov
				}, domain.ErrMissingField},
				{"refuses one with a requester of only spaces", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					ov := current(job)
					ov.By = " \t"
					return ov
				}, domain.ErrMissingField},
				{"refuses one with a requester of only zero-width characters", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					return signedLimit(current(job), "\u200b\u2060")
				}, domain.ErrMissingField},
				{"refuses one with a requester of only format characters", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					return signedLimit(current(job), "\ufeff\u200f")
				}, domain.ErrMissingField},
				{"refuses one with a requester of only punctuation", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					return signedLimit(current(job), "-_-")
				}, domain.ErrMissingField},
				{"refuses one with a requester of only a Braille blank", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					return signedLimit(current(job), "\u2800")
				}, domain.ErrMissingField},
				{"refuses one with an invisible requester on a stopped job", stoppedJob, func(job domain.Job) domain.LimitOverrideRequested {
					return signedLimit(current(job), "\u200b")
				}, decide.ErrStopped},
				{"refuses one with an invisible requester for another job", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					ov := signedLimit(current(job), "\u200b")
					ov.Job = "999-other"
					return ov
				}, decide.ErrOtherJob},
				{"refuses one that names no wait", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					ov := current(job)
					ov.WaitSeq = 0
					return ov
				}, domain.ErrMissingField},
				{"refuses one for an earlier wait", func() domain.Job {
					job := needsHumanJob()
					job.WaitSeq = 3
					return job
				}, func(job domain.Job) domain.LimitOverrideRequested {
					ov := current(job)
					ov.WaitSeq = 2
					return ov
				}, decide.ErrStaleWait},
				{"refuses one for a later wait", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					ov := current(job)
					ov.WaitSeq = 2
					return ov
				}, decide.ErrStaleWait},
				{"refuses one for another wait before a bad value", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					return limitOverride(domain.AuthorPerson, true, "retries", "0", job.WaitSeq+1, 2)
				}, decide.ErrStaleWait},
				{"refuses one on a stopped job", stoppedJob, current, decide.ErrStopped},
				{"refuses one on a rejected job", func() domain.Job { return unmergedSlice(domain.StageRejected) }, current, decide.ErrIllegalTransition},
				{"refuses one for another job", needsHumanJob, func(job domain.Job) domain.LimitOverrideRequested {
					ov := current(job)
					ov.Job = "999-other"
					return ov
				}, decide.ErrOtherJob},
			}
			for _, st := range []string{"intake", "spec", domain.StageSpecgate, "build", domain.StageGate, "review", "approve", domain.StageMerge, domain.StageDone, domain.StageNeedsInfo} {
				rows = append(rows, limitEventRow{"refuses one in " + st + " as an illegal transition", func() domain.Job { return unmergedSlice(st) }, current, decide.ErrIllegalTransition})
			}
			specs.Table(s, rows, func(r limitEventRow) string { return r.name }, func(ctx *specs.Context, r limitEventRow) {
				job := r.job()

				evs, err := decide.Decide(job, r.ev(job), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.Apply(job, evs)).ToEqual(job)
			})

			s.It("refuses a replay of an accepted one", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := usedUp(ctx, p)
				ov := raised(job, "human_reworks", "3", 12)
				evs, err := decide.Decide(job, ov, p)
				ctx.Expect(err).To(specs.BeNil())
				after := decide.Apply(job, evs)

				again, err := decide.Decide(after, ov, p)

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				ctx.Expect(again).To(specs.BeEmpty())
			})

			s.It("refuses a stale one after the job left needs-human and came back", func(ctx *specs.Context) {
				p := reworksPipeline(2)
				job := waitingFor(ctx, domain.CauseEscalated)
				stale := raised(job, "human_reworks", "5", 2)
				job = escalateAgain(ctx, reworkOnce(ctx, job, p), p)

				evs, err := decide.Decide(job, stale, p)

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(job.Limits).ToEqual(domain.Limits{})
			})
		})
	})
}
