package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// okFor is a person's checkpoint OK for wait seq.
func okFor(seq, minute int) domain.HumanApproved {
	return domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: seq, At: at(minute)}
}

// answerFor is a person's answer for wait seq.
func answerFor(seq, minute int) domain.MessageReceived {
	return answering(reply(domain.AuthorPerson, true, minute), seq)
}

// atCheckpoint is an initiative at the checkpoint in wait seq.
func atCheckpoint(seq int) domain.Initiative {
	in := threeSlices(domain.CheckpointEverySlice)
	in.Stage, in.WaitSeq = domain.InitiativeCheckpoint, seq
	return in
}

// stuckIn is an initiative in needs-human in wait seq.
func stuckIn(seq int) domain.Initiative {
	in := stuckAt()
	in.WaitSeq = seq
	return in
}

// answeredThenCheckpoint is an initiative that stopped in needs-human (wait
// 1), got its answer and then reached the checkpoint (wait 2), walked through
// decide.
func answeredThenCheckpoint(ctx *specs.Context) domain.Initiative {
	in := stuck(ctx, domain.CheckpointEverySlice)
	in = replan(ctx, in, answerFor(1, 3), []domain.Event{answered(3)})
	return replan(ctx, in, continued(4), []domain.Event{
		domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(4)},
	})
}

type initiativeWaitRow struct {
	name string
	in   domain.Initiative
	ev   domain.Event
	want error
}

type initiativeFoldRow struct {
	name string
	evs  []domain.Event
	want int
}

func TestInitiativeWait(t *testing.T) {
	specs.Describe(t, "the initiative's WaitSeq", func(s *specs.Spec) {
		s.It("starts at 0", func(ctx *specs.Context) {
			ctx.Expect(domain.Initiative{}.WaitSeq).ToEqual(0)
			ctx.Expect(threeSlices(domain.CheckpointEverySlice).WaitSeq).ToEqual(0)
		})

		s.When("events are folded", func(s *specs.Spec) {
			reached := domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(2)}
			raised := domain.InitiativeNeedsHumanRaised{Job: jobID, Initiative: initiativeID, Reason: question, At: at(2)}
			otherReached := reached
			otherReached.Initiative = "018-other"
			otherRaised := raised
			otherRaised.Initiative = "018-other"
			ended := domain.InitiativeEnded{Job: jobID, Initiative: initiativeID, At: at(5)}
			rows := []initiativeFoldRow{
				{"rises by one entering the checkpoint", []domain.Event{replanAsked(jobID, 1), reached}, 5},
				{"rises by one entering needs-human", []domain.Event{replanAsked(jobID, 1), raised}, 5},
				{"keeps it leaving the checkpoint", []domain.Event{replanAsked(jobID, 1), reached, nextSlice(3)}, 5},
				{"keeps it leaving needs-human", []domain.Event{replanAsked(jobID, 1), raised, answered(3)}, 5},
				{"counts each wait", []domain.Event{replanAsked(jobID, 1), raised, answered(3), reached, nextSlice(4)}, 6},
				{"keeps it closing", []domain.Event{replanAsked(jobID, 1), raised, answered(3), ended}, 5},
				{"keeps it after it closed", []domain.Event{replanAsked(jobID, 1), ended, reached, raised}, 4},
				{"ignores another initiative's waits", []domain.Event{replanAsked(jobID, 1), otherReached, otherRaised}, 4},
			}
			specs.Table(s, rows, func(r initiativeFoldRow) string { return r.name }, func(ctx *specs.Context, r initiativeFoldRow) {
				in := threeSlices(domain.CheckpointEverySlice)
				in.WaitSeq = 4

				ctx.Expect(decide.ApplyInitiative(in, r.evs).WaitSeq).ToEqual(r.want)
			})
		})

		s.When("a person's answer or OK names another wait", func(s *specs.Spec) {
			s.It("refuses a replayed checkpoint OK as stale, naming the expected wait, and starts no slice", func(ctx *specs.Context) {
				in := answeredThenCheckpoint(ctx)
				ctx.Expect(in.WaitSeq).ToEqual(2)

				evs, err := decide.Initiative(in, okFor(1, 5))

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				// It names the expected wait and nothing else: no stage, no reason.
				ctx.Expect(err.Error()).ToEqual("action for another wait: HumanApproved for wait 1, expected wait 2")
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.ApplyInitiative(in, evs)).ToEqual(in)

				in = replan(ctx, in, okFor(2, 5), []domain.Event{nextSlice(5)})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeActive)
			})

			s.It("refuses a replayed answer as stale, naming the expected wait, and does not rerun the replan", func(ctx *specs.Context) {
				in := stuck(ctx, domain.CheckpointEverySlice)
				in = replan(ctx, in, answerFor(1, 3), []domain.Event{answered(3)})
				in = replan(ctx, in, needsHumanReplan(4, "still unsure"), []domain.Event{
					domain.InitiativeNeedsHumanRaised{Job: jobID, Initiative: initiativeID, Reason: "still unsure", At: at(4)},
				})
				ctx.Expect(in.WaitSeq).ToEqual(2)

				evs, err := decide.Initiative(in, answerFor(1, 5))

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				ctx.Expect(err.Error()).ToEqual("action for another wait: MessageReceived for wait 1, expected wait 2")
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.ApplyInitiative(in, evs)).ToEqual(in)

				in = replan(ctx, in, answerFor(2, 5), []domain.Event{answered(5)})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
			})

			rows := []initiativeWaitRow{
				{"refuses an OK for a later wait at the checkpoint", atCheckpoint(2), okFor(3, 5), decide.ErrStaleWait},
				{"refuses an answer for a later wait in needs-human", stuckIn(2), answerFor(3, 5), decide.ErrStaleWait},
				{"refuses an OK with token zero at the checkpoint as missing", atCheckpoint(2), okFor(0, 5), domain.ErrMissingField},
				{"refuses an answer with token zero in needs-human as missing", stuckIn(2), answerFor(0, 5), domain.ErrMissingField},
				{"refuses every OK on a checkpoint stored without a counter", atCheckpoint(0), okFor(1, 5), decide.ErrStaleWait},
				{"refuses a zero OK on a checkpoint stored without a counter", atCheckpoint(0), okFor(0, 5), domain.ErrMissingField},
				{"refuses every answer on a needs-human stored without a counter", stuckIn(0), answerFor(1, 5), decide.ErrStaleWait},
				{"refuses a zero answer on a needs-human stored without a counter", stuckIn(0), answerFor(0, 5), domain.ErrMissingField},
			}
			specs.Table(s, rows, func(r initiativeWaitRow) string { return r.name }, func(ctx *specs.Context, r initiativeWaitRow) {
				evs, err := decide.Initiative(r.in, r.ev)

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.ApplyInitiative(r.in, evs)).ToEqual(r.in)
			})
		})

		s.When("the token is checked", func(s *specs.Spec) {
			replanning := stuckIn(2)
			replanning.Stage, replanning.Reason = domain.InitiativeReplan, ""
			rows := []initiativeWaitRow{
				{"ignores a bot's stale answer without error", stuckIn(2), answering(reply(domain.AuthorBot, true, 5), 1), nil},
				{"ignores a bot's answer with token zero without error", stuckIn(2), answering(reply(domain.AuthorBot, true, 5), 0), nil},
				{"ignores a stale answer from a person not allowed without error", stuckIn(2), answering(reply(domain.AuthorPerson, false, 5), 1), nil},
				{"refuses an answer with a blank author before its stale token", stuckIn(2), signed(answerFor(1, 5), " "), domain.ErrMissingField},
				{"refuses an OK with a blank author before its stale token", atCheckpoint(2), domain.HumanApproved{Job: jobID, By: "", WaitSeq: 1, At: at(5)}, domain.ErrMissingField},
				{"refuses a stale OK as stale before looking at the stage", replanning, okFor(1, 5), decide.ErrStaleWait},
				{"refuses a stale answer as stale before looking at the stage", replanning, answerFor(1, 5), decide.ErrStaleWait},
				{"refuses a current OK by the stage", replanning, okFor(2, 5), decide.ErrIllegalTransition},
				{"refuses a current answer by the stage", atCheckpoint(2), answerFor(2, 5), decide.ErrIllegalTransition},
				{"refuses another slice's stale OK as another initiative's", atCheckpoint(2), domain.HumanApproved{Job: secondSlice, By: "pablogore", WaitSeq: 1, At: at(5)}, decide.ErrOtherInitiative},
			}
			specs.Table(s, rows, func(r initiativeWaitRow) string { return r.name }, func(ctx *specs.Context, r initiativeWaitRow) {
				evs, err := decide.Initiative(r.in, r.ev)

				if r.want == nil {
					ctx.Expect(err).To(specs.BeNil())
				} else {
					ctx.Expect(err).To(specs.MatchError(r.want))
				}
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.ApplyInitiative(r.in, evs)).ToEqual(r.in)
			})
		})
	})
}
