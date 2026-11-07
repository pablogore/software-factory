package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/ports"
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

// recorder is a subscriber that keeps what it handled and counts every try.
// fail, when set, decides which events it refuses.
type recorder struct {
	got   []domain.Event
	tries int
	fail  func(domain.Event) bool
}

var errHandler = errors.New("handler failed")

func (r *recorder) handle(_ context.Context, ev domain.Event) error {
	r.tries++
	if r.fail != nil && r.fail(ev) {
		return errHandler
	}
	r.got = append(r.got, ev)
	return nil
}

// clock is a ports.Clock that moves only when a test moves it.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newClock() *clock { return &clock{now: at(30)} }

// subscribed returns a bus over st and c with r subscribed as name to
// StageEntered.
func subscribed(ctx *specs.Context, st feedStore, c *clock, name string, r *recorder) *bus.Bus {
	b := bus.New(st, c)
	ctx.Expect(b.Subscribe(name, r.handle, "StageEntered")).To(specs.BeNil())
	return b
}

// waits are the pauses before each retry of a failed event.
var waits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}

// exhaust lets every retry of b fall due and run, so whatever keeps failing
// ends up dead.
func exhaust(ctx *specs.Context, b *bus.Bus, c *clock) {
	for _, w := range waits {
		c.advance(w)
		ctx.Expect(b.Dispatch(context.Background())).To(specs.BeNil())
	}
}

// deliveries is what st saved about subscriber's failed deliveries.
func deliveries(ctx *specs.Context, st feedStore, subscriber string) []ports.Delivery {
	ds, err := st.Deliveries(context.Background(), subscriber)
	ctx.Expect(err).To(specs.BeNil())
	return ds
}

// failsFor refuses every event of job id while *broken holds.
func failsFor(id domain.JobID, broken *bool) func(domain.Event) bool {
	return func(ev domain.Event) bool { return *broken && ev.JobID() == id }
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
		apply(ctx, st, jobID, domain.GateFailed{Job: jobID, Run: domain.ChecksRun{Command: "make check", ExitCode: 2}, At: at(3)})
		board := &recorder{}
		b := subscribed(ctx, st, newClock(), "board", board)

		ctx.Expect(b.Start(bg)).To(specs.BeNil())

		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2), entered(jobID, "build", 3)})
	})

	s.It("delivers on start the events pending from before it, ahead of any published after it", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		board := &recorder{}
		b := subscribed(ctx, st, newClock(), "board", board)
		apply(ctx, st, "job-2", ready("job-2", 2))

		ctx.Expect(b.Dispatch(bg)).To(specs.MatchError(bus.ErrNotStarted))
		ctx.Expect(board.got).To(specs.BeEmpty())

		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2)})

		apply(ctx, st, jobID, domain.GatePassed{Job: jobID, Run: domain.ChecksRun{Command: "make check"}, At: at(3)})
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2), entered(jobID, "review", 3)})
	})

	s.It("after a restart, skips the events a subscriber already processed and delivers only the rest", func(ctx *specs.Context) {
		st, restart := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		ctx.Expect(subscribed(ctx, st, newClock(), "board", &recorder{}).Start(bg)).To(specs.BeNil())
		apply(ctx, st, "job-2", ready("job-2", 2))

		st = restart()
		board, inbox := &recorder{}, &recorder{}
		b := subscribed(ctx, st, newClock(), "board", board)
		ctx.Expect(b.Subscribe("inbox", inbox.handle, "StageEntered")).To(specs.BeNil())
		ctx.Expect(b.Start(bg)).To(specs.BeNil())

		ctx.Expect(board.got).ToEqual([]domain.Event{entered("job-2", domain.StageGate, 2)})
		ctx.Expect(inbox.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2)})
	})

	s.It("on start tries a failing event once without waiting, holds back that job's later events for it, and retries it after a restart once due", func(ctx *specs.Context) {
		st, restart := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		apply(ctx, st, "job-2", ready("job-2", 2))
		apply(ctx, st, jobID, domain.GatePassed{Job: jobID, Run: domain.ChecksRun{Command: "make check"}, At: at(3)})
		broken := true
		c := newClock()
		failing, inbox := &recorder{fail: failsFor(jobID, &broken)}, &recorder{}
		b := subscribed(ctx, st, c, "board", failing)
		ctx.Expect(b.Subscribe("inbox", inbox.handle, "StageEntered")).To(specs.BeNil())

		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		ctx.Expect(failing.tries).ToEqual(2)
		ctx.Expect(failing.got).ToEqual([]domain.Event{entered("job-2", domain.StageGate, 2)})
		ctx.Expect(inbox.got).To(specs.HaveLen(3))
		ds := deliveries(ctx, st, "board")
		ctx.Expect(ds).To(specs.HaveLen(1))
		ctx.Expect(ds[0].Job).ToEqual(jobID)
		ctx.Expect(ds[0].Failure).ToEqual(failure(1, c.now.Add(time.Second)))
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(failing.tries).ToEqual(2)

		st = restart()
		c.advance(time.Second)
		board := &recorder{}
		b = bus.New(st, c)
		ctx.Expect(b.Subscribe("board", board.handle, "StageEntered")).To(specs.BeNil())
		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered(jobID, "review", 3)})
		ctx.Expect(deliveries(ctx, st, "board")).To(specs.BeEmpty())
	})

	s.It("retries a failing subscriber after 1s, 2s, 4s, 8s and 16s, then gives up into dead events, while another subscriber gets the event", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		c := newClock()
		failing, inbox := &recorder{fail: func(domain.Event) bool { return true }}, &recorder{}
		b := subscribed(ctx, st, c, "board", failing)
		ctx.Expect(b.Subscribe("inbox", inbox.handle, "StageEntered")).To(specs.BeNil())
		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		ctx.Expect(inbox.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1)})

		for i, w := range waits {
			ctx.Expect(deliveries(ctx, st, "board")[0].Failure).ToEqual(failure(i+1, c.now.Add(w)))
			c.advance(w - time.Millisecond)
			ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
			ctx.Expect(failing.tries).ToEqual(i + 1)
			c.advance(time.Millisecond)
			ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
			ctx.Expect(failing.tries).ToEqual(i + 2)
		}

		ds := deliveries(ctx, st, "board")
		ctx.Expect(ds).To(specs.HaveLen(1))
		ctx.Expect(ds[0].Job).ToEqual(jobID)
		ctx.Expect(ds[0].Failure).ToEqual(dead(6))
		c.advance(time.Hour)
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(failing.tries).ToEqual(6)
		ctx.Expect(failing.got).To(specs.BeEmpty())
		ctx.Expect(inbox.got).To(specs.HaveLen(1))
	})

	s.It("processes a retried event once its handler succeeds, and clears its delivery", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		c := newClock()
		board := &recorder{}
		board.fail = func(domain.Event) bool { return board.tries < 3 }
		b := subscribed(ctx, st, c, "board", board)

		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		exhaust(ctx, b, c)

		ctx.Expect(board.tries).ToEqual(3)
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1)})
		ctx.Expect(deliveries(ctx, st, "board")).To(specs.BeEmpty())
	})

	s.It("holds a dead event's later events for that subscriber and that job only, until a person retries it", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		apply(ctx, st, "job-2", ready("job-2", 2))
		broken := true
		c := newClock()
		board, inbox := &recorder{fail: failsFor(jobID, &broken)}, &recorder{}
		b := subscribed(ctx, st, c, "board", board)
		ctx.Expect(b.Subscribe("inbox", inbox.handle, "StageEntered")).To(specs.BeNil())
		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		exhaust(ctx, b, c)
		ds := deliveries(ctx, st, "board")
		ctx.Expect(ds).To(specs.HaveLen(1))
		ctx.Expect(ds[0].Dead).To(specs.BeTrue())

		apply(ctx, st, jobID, domain.GatePassed{Job: jobID, Run: domain.ChecksRun{Command: "make check"}, At: at(3)})
		apply(ctx, st, "job-2", domain.GatePassed{Job: "job-2", Run: domain.ChecksRun{Command: "make check"}, At: at(4)})
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered("job-2", domain.StageGate, 2), entered("job-2", "review", 4)})
		ctx.Expect(inbox.got).To(specs.HaveLen(4))

		broken = false
		ctx.Expect(st.(ports.DeadEvents).RetryDead(bg, "board", ds[0].Seq)).To(specs.BeNil())
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(board.got[2:]).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered(jobID, "review", 3)})
		ctx.Expect(deliveries(ctx, st, "board")).To(specs.BeEmpty())
	})

	s.It("releases a dead event's later events, without it, once a person discards it", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		broken := true
		c := newClock()
		board := &recorder{fail: failsFor(jobID, &broken)}
		b := subscribed(ctx, st, c, "board", board)
		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		exhaust(ctx, b, c)
		apply(ctx, st, jobID, domain.GatePassed{Job: jobID, Run: domain.ChecksRun{Command: "make check"}, At: at(3)})
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(board.got).To(specs.BeEmpty())

		broken = false
		ctx.Expect(st.(ports.DeadEvents).DiscardDead(bg, "board", deliveries(ctx, st, "board")[0].Seq)).To(specs.BeNil())
		ctx.Expect(b.Dispatch(bg)).To(specs.BeNil())
		ctx.Expect(board.got).ToEqual([]domain.Event{entered(jobID, "review", 3)})
		ctx.Expect(deliveries(ctx, st, "board")).To(specs.BeEmpty())
	})

	s.It("delivers in the same run an event a handler saves", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		var got []domain.Event
		b := bus.New(st, newClock())
		gate := func(_ context.Context, ev domain.Event) error {
			got = append(got, ev)
			if ev.(domain.StageEntered).Stage == domain.StageGate {
				apply(ctx, st, jobID, domain.GatePassed{Job: jobID, Run: domain.ChecksRun{Command: "make check"}, At: at(2)})
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
		b := subscribed(ctx, st, newClock(), "board", r)

		ctx.Expect(b.Subscribe("inbox", r.handle)).To(specs.MatchError(bus.ErrSubscriber))
		ctx.Expect(b.Subscribe("board", r.handle, "StageEntered")).To(specs.MatchError(bus.ErrSubscriber))
		ctx.Expect(b.Start(bg)).To(specs.BeNil())
		ctx.Expect(b.Subscribe("inbox", r.handle, "StageEntered")).To(specs.MatchError(bus.ErrStarted))
		ctx.Expect(b.Start(bg)).To(specs.MatchError(bus.ErrStarted))
	})
}
