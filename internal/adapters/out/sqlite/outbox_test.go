package sqlite_test

import (
	"context"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// outboxStore is a store that also keeps the actions Apply saves.
type outboxStore interface {
	store
	ports.Outbox
}

func TestOutbox(t *testing.T) {
	specs.Describe(t, "sqlite.Store outbox", func(s *specs.Spec) {
		outboxContract(s, func(ctx *specs.Context) outboxStore { return openStore(ctx) })
	})
	specs.Describe(t, "memstore.Store (fake) outbox", func(s *specs.Spec) {
		outboxContract(s, func(ctx *specs.Context) outboxStore { return newMemstore(ctx) })
	})
}

func comment(key string) ports.Action {
	return ports.Action{Key: key, Kind: "comment", Payload: `{"body":"build ready"}`}
}

// queued is a as the outbox keeps it for jobID before any delivery.
func queued(a ports.Action) ports.QueuedAction {
	return ports.QueuedAction{Job: jobID, Action: a}
}

// actions is what st keeps for job id.
func actions(ctx *specs.Context, st ports.Outbox, id domain.JobID) []ports.QueuedAction {
	qs, err := st.Actions(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	return qs
}

// outboxContract is how every store saves the actions a state change asks
// for: the SQLite store and the in-memory fake run it alike.
func outboxContract(s *specs.Spec, newStore func(ctx *specs.Context) outboxStore) {
	bg := context.Background()

	s.When("a state change asks for actions", func(s *specs.Spec) {
		s.It("saves them with the state, pending, due now and with no attempts", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			push := ports.Action{Key: "job-1/push/1", Kind: "push", Payload: "factory/job-1"}

			_, _, err := st.Apply(bg, jobID, v, pipeline(), built(domain.BuildReady, 1), comment("job-1/comment/1"), push)

			ctx.Expect(err).To(specs.BeNil())
			got, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got.Stage).ToEqual(domain.StageGate)
			want := []ports.QueuedAction{queued(comment("job-1/comment/1")), queued(push)}
			ctx.Expect(actions(ctx, st, jobID)).ToEqual(want)
			due, err := st.DueActions(bg, at(1))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(due).ToEqual(want)
		})

		s.It("keeps each job's actions apart", func(ctx *specs.Context) {
			st := newStore(ctx)
			createJobs(ctx, st)

			_, _, err := st.Apply(bg, "job-2", 1, pipeline(), ready("job-2", 1), comment("job-2/comment/1"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(actions(ctx, st, jobID)).To(specs.BeEmpty())
			ctx.Expect(actions(ctx, st, "job-2")).ToEqual([]ports.QueuedAction{{Job: "job-2", Action: comment("job-2/comment/1")}})
		})
	})

	s.When("the state change is not saved", func(s *specs.Spec) {
		s.It("saves no action when Decide refuses the event", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, v := created(ctx, st)

			_, _, err := st.Apply(bg, jobID, v, pipeline(), otherEvent{Job: jobID}, comment("job-1/comment/1"))

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(actions(ctx, st, jobID)).To(specs.BeEmpty())
		})

		s.It("saves no action when the job was saved since it was loaded", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			_, _, err := st.Apply(bg, jobID, v, pipeline(), built(domain.BuildReady, 1))
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = st.Apply(bg, jobID, v, pipeline(), domain.GatePassed{Job: jobID, At: at(2)}, comment("job-1/comment/1"))

			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))
			ctx.Expect(actions(ctx, st, jobID)).To(specs.BeEmpty())
		})

		s.It("saves no action when Decide returns no events", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			bot := domain.MessageReceived{Job: jobID, Provider: "github", Thread: "29", Author: "ci", AuthorKind: domain.AuthorBot, Text: "hi", WaitSeq: 1, At: at(1)}

			_, _, err := st.Apply(bg, jobID, v, pipeline(), bot, comment("job-1/comment/1"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(actions(ctx, st, jobID)).To(specs.BeEmpty())
		})

		rows := []invalidAction{
			{"without a key", ports.Action{Kind: "comment"}},
			{"without a kind", ports.Action{Key: "job-1/comment/1"}},
		}
		specs.Table(s, rows, func(r invalidAction) string {
			return "an action " + r.name + " is refused and saves neither the state, any event nor any action"
		}, func(ctx *specs.Context, r invalidAction) {
			st := newStore(ctx)
			job, v := created(ctx, st)

			_, _, err := st.Apply(bg, jobID, v, pipeline(), built(domain.BuildReady, 1), comment("job-1/comment/1"), r.act)

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			got, gotV, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(job)
			ctx.Expect(gotV).ToEqual(v)
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
			ctx.Expect(actions(ctx, st, jobID)).To(specs.BeEmpty())
		})
	})

	s.When("the same action is asked for twice with the same key", func(s *specs.Spec) {
		s.It("keeps one, without error, when both are in one state change", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, v := created(ctx, st)

			_, _, err := st.Apply(bg, jobID, v, pipeline(), built(domain.BuildReady, 1), comment("job-1/comment/1"), comment("job-1/comment/1"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(actions(ctx, st, jobID)).ToEqual([]ports.QueuedAction{queued(comment("job-1/comment/1"))})
		})

		s.It("keeps the first one, without error, when a later state change asks again", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			_, v, err := st.Apply(bg, jobID, v, pipeline(), built(domain.BuildReady, 1), comment("job-1/comment/1"))
			ctx.Expect(err).To(specs.BeNil())
			again := comment("job-1/comment/1")
			again.Payload = `{"body":"build ready again"}`

			_, _, err = st.Apply(bg, jobID, v, pipeline(), domain.GatePassed{Job: jobID, At: at(2)}, again)

			ctx.Expect(err).To(specs.BeNil())
			got, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got.Stage).ToEqual("review")
			ctx.Expect(actions(ctx, st, jobID)).ToEqual([]ports.QueuedAction{queued(comment("job-1/comment/1"))})
		})
	})

	s.When("an action's delivery is recorded", func(s *specs.Spec) {
		s.It("an unknown key is action not found", func(ctx *specs.Context) {
			st := newStore(ctx)

			ctx.Expect(st.MarkActionDelivered(bg, "nope")).To(specs.MatchError(ports.ErrActionNotFound))
			ctx.Expect(st.MarkActionFailed(bg, "nope", ports.ActionFailure{Attempts: 1})).To(specs.MatchError(ports.ErrActionNotFound))
			ctx.Expect(st.MarkActionRefused(bg, "nope", ports.ActionFailure{Attempts: 1}, at(1))).To(specs.MatchError(ports.ErrActionNotFound))
		})
	})
}

type invalidAction struct {
	name string
	act  ports.Action
}
