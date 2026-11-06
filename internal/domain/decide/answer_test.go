package decide_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

const (
	question = "S3 needs a decision on the API shape before it can be cut"
	answer   = "keep the REST shape; drop the gRPC slice"
)

// stuck is a three-slice initiative whose replan after its first slice asked
// for a person.
func stuck(ctx *specs.Context, checkpoint domain.Checkpoint) domain.Initiative {
	_, in := mergeSlice(ctx, threeSlices(checkpoint))
	return replan(ctx, in, needsHumanReplan(2, question), []domain.Event{
		domain.InitiativeNeedsHumanRaised{Job: jobID, Initiative: initiativeID, Reason: question, At: at(2)},
	})
}

// reply is a message on the stuck slice's thread from an author of kind.
func reply(kind domain.AuthorKind, allowed bool, minute int) domain.MessageReceived {
	m := message(kind, allowed, minute)
	m.Text = answer
	return m
}

func answered(minute int) domain.InitiativeAnswered {
	return domain.InitiativeAnswered{Job: jobID, Initiative: initiativeID, By: "pablogore", Text: answer, At: at(minute)}
}

// stuckAt is a needs-human initiative built by hand, for tables.
func stuckAt() domain.Initiative {
	in := threeSlices(domain.CheckpointEverySlice)
	in.Stage, in.Reason = domain.InitiativeNeedsHuman, question
	return in
}

type answerRow struct {
	name string
	run  func(ctx *specs.Context)
}

func TestInitiativeAnswer(t *testing.T) {
	specs.Describe(t, "Decide on an answer to an initiative in needs-human", func(s *specs.Spec) {
		rows := []answerRow{
			{"replan NEEDS-HUMAN, then an allowed person's answer, ends in replan with the answer as input", func(ctx *specs.Context) {
				for _, checkpoint := range []domain.Checkpoint{domain.CheckpointEverySlice, domain.CheckpointAuto} {
					in := stuck(ctx, checkpoint)

					in = replan(ctx, in, reply(domain.AuthorPerson, true, 3), []domain.Event{answered(3)})

					ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
					ctx.Expect(in.Job).ToEqual(jobID)
					ctx.Expect(in.Reason).ToEqual("")
				}
			}},
			{"a direction-marked answer is still an answer", func(ctx *specs.Context) {
				in := stuck(ctx, domain.CheckpointEverySlice)
				d := direction(domain.AuthorPerson, true, 3)

				in = replan(ctx, in, d, []domain.Event{
					domain.InitiativeAnswered{Job: jobID, Initiative: initiativeID, By: "pablogore", Text: directionText, At: at(3)},
				})

				ctx.Expect(in.Stage).ToEqual(domain.InitiativeReplan)
			}},
			{"the replan after the answer decides as any replan does", func(ctx *specs.Context) {
				in := stuck(ctx, domain.CheckpointEverySlice)
				in = replan(ctx, in, reply(domain.AuthorPerson, true, 3), []domain.Event{answered(3)})

				in = replan(ctx, in, continued(4), []domain.Event{
					domain.CheckpointReached{Job: jobID, Initiative: initiativeID, At: at(4)},
				})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeCheckpoint)

				in = stuck(ctx, domain.CheckpointAuto)
				in = replan(ctx, in, reply(domain.AuthorPerson, true, 3), []domain.Event{answered(3)})
				in = replan(ctx, in, needsHumanReplan(4, "still unsure"), []domain.Event{
					domain.InitiativeNeedsHumanRaised{Job: jobID, Initiative: initiativeID, Reason: "still unsure", At: at(4)},
				})
				ctx.Expect(in.Stage).ToEqual(domain.InitiativeNeedsHuman)
				ctx.Expect(in.Reason).ToEqual("still unsure")
			}},
			{"the slice job's own events never pull it out of needs-human", func(ctx *specs.Context) {
				in := stuck(ctx, domain.CheckpointEverySlice)
				job := sliceJob()
				job.Stage, job.WaitSeq = domain.StageDone, 1
				// The same answer reaches the merged slice too, which reopens it.
				reopened, err := decide.Decide(job, reply(domain.AuthorPerson, true, 3), defaultPipeline())
				ctx.Expect(err).To(specs.BeNil())

				ctx.Expect(decide.ApplyInitiative(in, reopened)).ToEqual(in)
				ctx.Expect(decide.ApplyInitiative(in, []domain.Event{
					replanAsked(jobID, 3),
					domain.SliceDropped{Job: jobID, Initiative: initiativeID, By: "pablogore", Text: directionText, At: at(3)},
					domain.SliceStarted{Job: secondSlice, Initiative: initiativeID, At: at(3)},
					domain.StageEntered{Job: jobID, Stage: domain.StageDone, At: at(3)},
					domain.NeedsHuman{Job: jobID, Reason: "approver escalated", At: at(3)},
					domain.MergeRequested{Job: jobID, At: at(3)},
				})).ToEqual(in)
			}},
		}
		specs.Table(s, rows, func(r answerRow) string { return r.name }, func(ctx *specs.Context, r answerRow) {
			r.run(ctx)
		})

		s.When("the message is not an allowed person's", func(s *specs.Spec) {
			type ignoredRow struct {
				name string
				ev   domain.MessageReceived
			}
			rows := []ignoredRow{
				{"ignores a bot", reply(domain.AuthorBot, true, 3)},
				{"ignores CI", reply(domain.AuthorCI, true, 3)},
				{"ignores an unknown author kind", reply("", true, 3)},
				{"ignores a person not allowed", reply(domain.AuthorPerson, false, 3)},
				{"ignores a direction from a person not allowed", direction(domain.AuthorPerson, false, 3)},
			}
			specs.Table(s, rows, func(r ignoredRow) string { return r.name }, func(ctx *specs.Context, r ignoredRow) {
				in := stuckAt()

				evs, err := decide.Initiative(in, r.ev)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.ApplyInitiative(in, evs)).ToEqual(in)
			})
		})

		s.When("the event is not an answer to the needs-human initiative", func(s *specs.Spec) {
			type refusedRow struct {
				name string
				in   domain.Initiative
				ev   domain.Event
				want error
			}
			in := func(stage string) domain.Initiative {
				in := threeSlices(domain.CheckpointEverySlice)
				in.Stage = stage
				return in
			}
			fromOtherSlice := reply(domain.AuthorPerson, true, 3)
			fromOtherSlice.Job = secondSlice
			rows := []refusedRow{
				{"refuses an answer while a slice runs", in(domain.InitiativeActive), reply(domain.AuthorPerson, true, 3), decide.ErrIllegalTransition},
				{"refuses an answer while replanning", in(domain.InitiativeReplan), reply(domain.AuthorPerson, true, 3), decide.ErrIllegalTransition},
				{"refuses an answer at the checkpoint", in(domain.InitiativeCheckpoint), reply(domain.AuthorPerson, true, 3), decide.ErrIllegalTransition},
				{"refuses an answer once closed", in(domain.InitiativeClosed), reply(domain.AuthorPerson, true, 3), decide.ErrIllegalTransition},
				{"refuses an answer on another slice job's thread", stuckAt(), fromOtherSlice, decide.ErrOtherInitiative},
				{"refuses a replan CONTINUE in needs-human", stuckAt(), continued(3), decide.ErrIllegalTransition},
				{"refuses a replan DONE in needs-human", stuckAt(), doneReplan(3), decide.ErrIllegalTransition},
				{"refuses a replan NEEDS-HUMAN in needs-human", stuckAt(), needsHumanReplan(3, "again"), decide.ErrIllegalTransition},
				{"refuses a person's OK in needs-human", stuckAt(), domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(3)}, decide.ErrIllegalTransition},
				{"refuses the slice's merge in needs-human", stuckAt(), merged(3), decide.ErrIllegalTransition},
				{"refuses a stop request in needs-human", stuckAt(), stop("pablogore", 3), decide.ErrIllegalTransition},
				{"refuses a missing event in needs-human", stuckAt(), nil, decide.ErrIllegalTransition},
				{"refuses a nil message in needs-human", stuckAt(), (*domain.MessageReceived)(nil), decide.ErrIllegalTransition},
				{"refuses a message pointer in needs-human", stuckAt(), &domain.MessageReceived{Job: jobID, AuthorKind: domain.AuthorPerson, Allowed: true, Text: answer, At: at(3)}, decide.ErrIllegalTransition},
			}
			specs.Table(s, rows, func(r refusedRow) string { return r.name }, func(ctx *specs.Context, r refusedRow) {
				evs, err := decide.Initiative(r.in, r.ev)

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(evs).To(specs.BeEmpty())
				ctx.Expect(decide.ApplyInitiative(r.in, evs)).ToEqual(r.in)
			})
		})

		s.When("an answer is folded where it does not belong", func(s *specs.Spec) {
			otherJob := answered(3)
			otherJob.Job = secondSlice
			otherInitiative := answered(3)
			otherInitiative.Initiative = "018-other"
			rows := []foldRow{
				{"leaves an active initiative alone", threeSlices(domain.CheckpointAuto), []domain.Event{answered(3)}},
				{"leaves a replanning initiative alone", func() domain.Initiative {
					in := stuckAt()
					in.Stage, in.Reason = domain.InitiativeReplan, ""
					return in
				}(), []domain.Event{answered(3)}},
				{"leaves an initiative at the checkpoint alone", func() domain.Initiative {
					in := stuckAt()
					in.Stage, in.Reason = domain.InitiativeCheckpoint, ""
					return in
				}(), []domain.Event{answered(3)}},
				{"leaves a closed initiative closed", func() domain.Initiative {
					in := stuckAt()
					in.Stage, in.Reason = domain.InitiativeClosed, ""
					return in
				}(), []domain.Event{answered(3)}},
				{"ignores an answer naming another slice job", stuckAt(), []domain.Event{otherJob}},
				{"ignores an answer for another initiative", stuckAt(), []domain.Event{otherInitiative}},
			}
			specs.Table(s, rows, func(r foldRow) string { return r.name }, func(ctx *specs.Context, r foldRow) {
				ctx.Expect(decide.ApplyInitiative(r.in, r.evs)).ToEqual(r.in)
			})
		})
	})
}

// FuzzInitiativeNeedsHuman checks that an initiative in needs-human leaves it
// only on an allowed person's message that names who sent it, and then only
// for the replan; the same message without a name is refused. Any
// other event, a message from any other author or a missing one returns no
// events and leaves the initiative as it was, never panicking.
func FuzzInitiativeNeedsHuman(f *testing.F) {
	for i := range fuzzEvents() {
		f.Add(uint8(i), "pablogore", "keep REST", true, false)
	}
	f.Add(uint8(0), "", "", false, true)
	f.Fuzz(func(t *testing.T, event uint8, by, text string, allowed, marked bool) {
		in := stuckAt()
		before := in
		kinds := []domain.AuthorKind{domain.AuthorPerson, domain.AuthorBot, domain.AuthorCI, ""}
		m := domain.MessageReceived{Job: jobID, Provider: "github", Thread: "pr/88", Author: by,
			AuthorKind: kinds[int(event)%len(kinds)], Allowed: allowed, Direction: marked, Text: text, At: at(1)}
		evs := append(fuzzEvents(), m, continued(1), doneReplan(1), needsHumanReplan(1, "why"), replanAsked(jobID, 1))
		ev := evs[int(event)%len(evs)]

		out, err := decide.Initiative(in, ev)

		if !reflect.DeepEqual(in, before) {
			t.Fatalf("initiative changed: %+v, was %+v", in, before)
		}
		if err != nil && len(out) > 0 {
			t.Fatalf("events returned with an error: %v", out)
		}
		after := decide.ApplyInitiative(in, out)
		msg, ok := ev.(domain.MessageReceived)
		if ok && msg.Allowed && msg.AuthorKind == domain.AuthorPerson && strings.TrimSpace(msg.Author) == "" {
			if !errors.Is(err, domain.ErrMissingField) || len(out) > 0 {
				t.Fatalf("answer without a name gave %v, %v", out, err)
			}
			return
		}
		if ok && msg.Allowed && msg.AuthorKind == domain.AuthorPerson {
			if err != nil || after.Stage != domain.InitiativeReplan {
				t.Fatalf("allowed answer gave %v, %v, stage %q", out, err, after.Stage)
			}
			return
		}
		if len(out) > 0 || !reflect.DeepEqual(after, before) {
			t.Fatalf("%v moved the initiative: %v, %+v", ev, out, after)
		}
	})
}
