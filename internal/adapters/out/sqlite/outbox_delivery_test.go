package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/outbox"
	"github.com/pablogore/software-factory/internal/ports"
)

// The deliverer runs here, beside both stores, so the same scenarios prove
// it on SQLite and on the fake: platform tests cannot import adapters.
func TestOutboxDelivery(t *testing.T) {
	specs.Describe(t, "outbox.Deliverer on sqlite.Store", func(s *specs.Spec) {
		outboxDeliveryContract(s, func(ctx *specs.Context) (outboxStore, func() outboxStore) {
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			open := func() *sqlite.Store {
				st, err := sqlite.Open(context.Background(), path)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Cleanup(func() { _ = st.Close() })
				return st
			}
			first := open()
			return first, func() outboxStore {
				ctx.Expect(first.Close()).To(specs.BeNil())
				return open()
			}
		})
	})
	specs.Describe(t, "outbox.Deliverer on memstore.Store (fake)", func(s *specs.Spec) {
		outboxDeliveryContract(s, func(ctx *specs.Context) (outboxStore, func() outboxStore) {
			st := newMemstore(ctx)
			return st, func() outboxStore { return st }
		})
	})
}

var (
	errUnavailable = errors.New("503 service unavailable")
	errNetwork     = errors.New("dial tcp: connection refused")
)

// destination is a test ports.Destination: it keeps what it did and counts
// every try. While fails has errors left, each try takes the next one and
// does nothing.
type destination struct {
	done  []ports.Action
	tries int
	fails []error
}

func (d *destination) Deliver(_ context.Context, a ports.Action) error {
	d.tries++
	if len(d.fails) > 0 {
		err := d.fails[0]
		d.fails = d.fails[1:]
		return err
	}
	d.done = append(d.done, a)
	return nil
}

// asked saves a state change of newJob in st that asks for acts.
func asked(ctx *specs.Context, st outboxStore, acts ...ports.Action) {
	_, v := created(ctx, st)
	_, _, err := st.Apply(context.Background(), jobID, v, pipeline(), built(domain.BuildReady, 1), acts...)
	ctx.Expect(err).To(specs.BeNil())
}

// only is the one action st keeps for jobID.
func only(ctx *specs.Context, st outboxStore) ports.QueuedAction {
	qs := actions(ctx, st, jobID)
	ctx.Expect(qs).To(specs.HaveLen(1))
	return qs[0]
}

func outboxDeliveryContract(s *specs.Spec, open func(ctx *specs.Context) (outboxStore, func() outboxStore)) {
	bg := context.Background()

	s.It("delivers a saved action once and marks it delivered", func(ctx *specs.Context) {
		st, _ := open(ctx)
		asked(ctx, st, comment("job-1/comment/1"))
		dest, c := &destination{}, newClock()
		d := outbox.New(st, dest, c)

		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())
		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())

		ctx.Expect(dest.done).ToEqual([]ports.Action{comment("job-1/comment/1")})
		ctx.Expect(dest.tries).ToEqual(1)
		ctx.Expect(only(ctx, st)).ToEqual(ports.QueuedAction{Job: jobID, Action: comment("job-1/comment/1"), Delivered: true})
	})

	s.It("a 5xx failure reschedules the action after a growing wait, counting attempts, until it is delivered", func(ctx *specs.Context) {
		st, _ := open(ctx)
		asked(ctx, st, comment("job-1/comment/1"))
		dest, c := &destination{fails: []error{errUnavailable, errNetwork, errUnavailable}}, newClock()
		d := outbox.New(st, dest, c)

		for i, w := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
			ctx.Expect(d.Deliver(bg)).To(specs.BeNil())
			q := only(ctx, st)
			ctx.Expect(q.Attempts).ToEqual(i + 1)
			ctx.Expect(q.NextAttempt).ToEqual(c.now.Add(w))
			ctx.Expect(q.Delivered).ToEqual(false)

			// Not due before its wait is over.
			c.advance(w - time.Millisecond)
			ctx.Expect(d.Deliver(bg)).To(specs.BeNil())
			ctx.Expect(dest.tries).ToEqual(i + 1)
			c.advance(time.Millisecond)
		}
		ctx.Expect(only(ctx, st).LastError).ToEqual(errUnavailable.Error())

		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())

		ctx.Expect(dest.done).ToEqual([]ports.Action{comment("job-1/comment/1")})
		ctx.Expect(dest.tries).ToEqual(4)
		ctx.Expect(only(ctx, st)).ToEqual(ports.QueuedAction{
			Job: jobID, Action: comment("job-1/comment/1"), Delivered: true,
			ActionFailure: ports.ActionFailure{Attempts: 3, LastError: errUnavailable.Error()},
		})
	})

	s.It("gives up after the last retry, leaving the action failed with its attempts and never due again", func(ctx *specs.Context) {
		st, _ := open(ctx)
		asked(ctx, st, comment("job-1/comment/1"))
		dest, c := &destination{fails: []error{errUnavailable, errUnavailable, errUnavailable, errUnavailable, errUnavailable, errUnavailable, errUnavailable}}, newClock()
		d := outbox.New(st, dest, c)

		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())
		for _, w := range waits {
			c.advance(w)
			ctx.Expect(d.Deliver(bg)).To(specs.BeNil())
		}
		c.advance(time.Hour)
		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())

		ctx.Expect(dest.tries).ToEqual(6)
		ctx.Expect(dest.done).To(specs.BeEmpty())
		ctx.Expect(only(ctx, st)).ToEqual(ports.QueuedAction{
			Job: jobID, Action: comment("job-1/comment/1"),
			ActionFailure: ports.ActionFailure{Attempts: 6, LastError: errUnavailable.Error(), Failed: true},
		})
		due, err := st.DueActions(bg, c.now)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(due).To(specs.BeEmpty())
	})

	s.It("one action's failure does not hold back the others", func(ctx *specs.Context) {
		st, _ := open(ctx)
		asked(ctx, st, comment("job-1/comment/1"), comment("job-1/comment/2"))
		dest, c := &destination{fails: []error{errUnavailable}}, newClock()
		d := outbox.New(st, dest, c)

		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())

		ctx.Expect(dest.done).ToEqual([]ports.Action{comment("job-1/comment/2")})
		qs := actions(ctx, st, jobID)
		ctx.Expect(qs[0].Attempts).ToEqual(1)
		ctx.Expect(qs[1].Delivered).ToEqual(true)
	})

	s.It("after a restart, delivers what was pending and keeps the attempts and wait saved before it", func(ctx *specs.Context) {
		st, restart := open(ctx)
		asked(ctx, st, comment("job-1/comment/1"))
		c := newClock()
		ctx.Expect(outbox.New(st, &destination{fails: []error{errUnavailable}}, c).Deliver(bg)).To(specs.BeNil())

		st = restart()
		dest := &destination{}
		d := outbox.New(st, dest, c)
		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())
		ctx.Expect(dest.tries).ToEqual(0)
		ctx.Expect(only(ctx, st).Attempts).ToEqual(1)

		c.advance(time.Second)
		ctx.Expect(d.Deliver(bg)).To(specs.BeNil())

		ctx.Expect(dest.done).ToEqual([]ports.Action{comment("job-1/comment/1")})
		ctx.Expect(only(ctx, st).Delivered).ToEqual(true)
	})
}
