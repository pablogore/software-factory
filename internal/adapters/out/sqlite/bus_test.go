package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
)

// The bus runs here, beside both stores, so the same scenarios prove it on
// SQLite and on the fake: platform tests cannot import adapters.
func TestBus(t *testing.T) {
	specs.Describe(t, "bus.Bus on sqlite.Store", func(s *specs.Spec) {
		busContract(s, sqliteRestartable)
	})
	specs.Describe(t, "bus.Bus on memstore.Store (fake)", func(s *specs.Spec) {
		busContract(s, memstoreRestartable)
	})
}

// recorder is a subscriber that keeps what it was handed. fail, when set,
// decides which events it refuses.
type recorder struct {
	got  []domain.Event
	fail func(domain.Event) bool
}

var errHandler = errors.New("handler failed")

func (r *recorder) handle(_ context.Context, ev domain.Event) error {
	if r.fail != nil && r.fail(ev) {
		return errHandler
	}
	r.got = append(r.got, ev)
	return nil
}

// subscribed returns a bus over st with r subscribed as name to StageEntered.
func subscribed(ctx *specs.Context, st feedStore, name string, r *recorder) *bus.Bus {
	b := bus.New(st)
	ctx.Expect(b.Subscribe(name, r.handle, "StageEntered")).To(specs.BeNil())
	return b
}

func busContract(s *specs.Spec, open restartable) {
	bg := context.Background()

	s.It("delivers every saved event a subscriber registered for, in saved order", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		apply(ctx, st, "job-2", ready("job-2", 2))
		// RoundStarted, which board did not register for, comes before this
		// StageEntered.
		apply(ctx, st, jobID, domain.GateFailed{Job: jobID, At: at(3)})
		board := &recorder{}
		b := subscribed(ctx, st, "board", board)

		ctx.Expect(b.Start(bg)).To(specs.BeNil())

		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2), entered(jobID, "build", 3)})
	})

	s.It("delivers on start the events pending from before it, ahead of any published after it", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		board := &recorder{}
		b := subscribed(ctx, st, "board", board)
		apply(ctx, st, "job-2", ready("job-2", 2))

		ctx.Expect(b.Dispatch(bg)).To(specs.MatchError(bus.ErrNotStarted))
		ctx.Expect(board.got).To(specs.BeEmpty())

		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2)})

		apply(ctx, st, jobID, domain.GatePassed{Job: jobID, At: at(3)})
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2), entered(jobID, "review", 3)})
	})

	s.It("after a restart, skips the events a subscriber already processed and delivers only the rest", func(ctx *specs.Context) {
		st, restart := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		ctx.Expect(subscribed(ctx, st, "board", &recorder{}).Start(bg)).To(specs.BeNil())
		apply(ctx, st, "job-2", ready("job-2", 2))

		st = restart()
		board, inbox := &recorder{}, &recorder{}
		b := subscribed(ctx, st, "board", board)
		ctx.Expect(b.Subscribe("inbox", inbox.handle, "StageEntered")).To(specs.BeNil())
		ctx.Expect(b.Start(bg)).To(specs.BeNil())

		ctx.Expect(board.got).ToEqual([]domain.Event{entered("job-2", domain.StageGate, 2)})
		ctx.Expect(inbox.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2)})
	})

	s.It("leaves pending an event whose handler failed, holds back the subscriber's later ones, and redelivers it after a restart", func(ctx *specs.Context) {
		st, restart := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		apply(ctx, st, "job-2", ready("job-2", 2))
		failing := &recorder{fail: func(ev domain.Event) bool { return ev.JobID() == jobID }}
		inbox := &recorder{}
		b := subscribed(ctx, st, "board", failing)
		ctx.Expect(b.Subscribe("inbox", inbox.handle, "StageEntered")).To(specs.BeNil())

		ctx.Expect(b.Start(bg)).To(specs.MatchError(errHandler))
		ctx.Expect(failing.got).To(specs.BeEmpty())
		ctx.Expect(inbox.got).To(specs.HaveLen(2))
		ctx.Expect(b.Dispatch(bg)).To(specs.MatchError(bus.ErrNotStarted))

		board := &recorder{}
		ctx.Expect(subscribed(ctx, restart(), "board", board).Start(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2)})
	})

	s.It("delivers in the same run an event a handler saves", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		var got []domain.Event
		b := bus.New(st)
		gate := func(_ context.Context, ev domain.Event) error {
			got = append(got, ev)
			if ev.(domain.StageEntered).Stage == domain.StageGate {
				apply(ctx, st, jobID, domain.GatePassed{Job: jobID, At: at(2)})
			}
			return nil
		}
		ctx.Expect(b.Subscribe("gate", gate, "StageEntered")).To(specs.BeNil())

		ctx.Expect(b.Start(bg)).To(specs.BeNil())

		ctx.Expect(got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered(jobID, "review", 2)})
	})

	s.It("refuses a subscriber without events, a name taken, and any subscriber or second start once started", func(ctx *specs.Context) {
		st, _ := open(ctx)
		r := &recorder{}
		b := subscribed(ctx, st, "board", r)

		ctx.Expect(b.Subscribe("inbox", r.handle)).To(specs.MatchError(bus.ErrSubscriber))
		ctx.Expect(b.Subscribe("board", r.handle, "StageEntered")).To(specs.MatchError(bus.ErrSubscriber))
		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		ctx.Expect(b.Subscribe("inbox", r.handle, "StageEntered")).To(specs.MatchError(bus.ErrStarted))
		ctx.Expect(b.Start(bg)).To(specs.MatchError(bus.ErrStarted))
	})
}
