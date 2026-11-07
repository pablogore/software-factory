package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// feedStore is a store that also feeds the bus.
type feedStore interface {
	store
	ports.EventFeed
}

// restartable opens a store and returns a function that opens it again as a
// restarted factoryd would: the SQLite file is closed and reopened, the fake
// keeps the same memory.
type restartable func(ctx *specs.Context) (feedStore, func() feedStore)

func sqliteRestartable(ctx *specs.Context) (feedStore, func() feedStore) {
	path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
	open := func() *sqlite.Store {
		st, err := sqlite.Open(context.Background(), path)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Cleanup(func() { _ = st.Close() })
		return st
	}
	first := open()
	return first, func() feedStore {
		ctx.Expect(first.Close()).To(specs.BeNil())
		return open()
	}
}

func memstoreRestartable(ctx *specs.Context) (feedStore, func() feedStore) {
	st := newMemstore(ctx)
	return st, func() feedStore { return st }
}

func TestEventFeed(t *testing.T) {
	specs.Describe(t, "sqlite.Store event feed", func(s *specs.Spec) {
		feedContract(s, sqliteRestartable)
	})
	specs.Describe(t, "memstore.Store (fake) event feed", func(s *specs.Spec) {
		feedContract(s, memstoreRestartable)
	})
}

// apply loads job id from st and applies ev to it, so its events are saved.
func apply(ctx *specs.Context, st store, id domain.JobID, ev domain.Event) {
	_, v, err := st.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	_, _, err = st.Apply(context.Background(), id, v, pipeline(), ev)
	ctx.Expect(err).To(specs.BeNil())
}

// createJobs creates newJob and a second job, job-2, in st.
func createJobs(ctx *specs.Context, st store) {
	other := newJob()
	other.ID = "job-2"
	ctx.Expect(st.Create(context.Background(), newJob())).To(specs.BeNil())
	ctx.Expect(st.Create(context.Background(), other)).To(specs.BeNil())
}

func ready(id domain.JobID, minute int) domain.ResultSubmitted {
	return domain.ResultSubmitted{Job: id, Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady, Summary: "done"}, At: at(minute)}
}

func entered(id domain.JobID, stage string, minute int) domain.StageEntered {
	return domain.StageEntered{Job: id, Stage: stage, At: at(minute)}
}

// events lists what the feed handed out, without the seqs.
func events(saved []ports.Saved) []domain.Event {
	evs := []domain.Event{}
	for _, sv := range saved {
		evs = append(evs, sv.Event)
	}
	return evs
}

// feedContract is the behavior every EventFeed shares.
func feedContract(s *specs.Spec, open restartable) {
	bg := context.Background()

	s.It("lists every job's events with the given names in saved order, with rising seqs and their dates", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		apply(ctx, st, "job-2", ready("job-2", 2))
		apply(ctx, st, jobID, domain.GatePassed{Job: jobID, At: at(3)})

		got, err := st.Pending(bg, "board", []string{"StageEntered"})

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(events(got)).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1), entered("job-2", domain.StageGate, 2), entered(jobID, "review", 3)})
		ctx.Expect(got[0].At).ToEqual(at(1))
		ctx.Expect(got[0].Seq < got[1].Seq && got[1].Seq < got[2].Seq).To(specs.BeTrue())
	})

	s.It("leaves out events with other names, and lists nothing for no names", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		// A failed gate in round 1 saves RoundStarted, then StageEntered.
		apply(ctx, st, jobID, domain.GateFailed{Job: jobID, At: at(2)})

		got, err := st.Pending(bg, "board", []string{"RoundStarted"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.HaveLen(1))
		ctx.Expect(got[0].Event.EventName()).ToEqual("RoundStarted")

		got, err = st.Pending(bg, "board", nil)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.BeEmpty())
	})

	s.It("stops listing an event for the subscriber that marked it, and only for it", func(ctx *specs.Context) {
		st, _ := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		apply(ctx, st, "job-2", ready("job-2", 2))
		first, err := st.Pending(bg, "board", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())

		ctx.Expect(st.MarkProcessed(bg, "board", first[0].Seq)).To(specs.BeNil())
		ctx.Expect(st.MarkProcessed(bg, "board", first[0].Seq)).To(specs.BeNil())

		got, err := st.Pending(bg, "board", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(events(got)).ToEqual([]domain.Event{entered("job-2", domain.StageGate, 2)})
		got, err = st.Pending(bg, "inbox", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.HaveLen(2))
	})

	s.It("keeps what was marked processed across a restart", func(ctx *specs.Context) {
		st, restart := open(ctx)
		createJobs(ctx, st)
		apply(ctx, st, jobID, ready(jobID, 1))
		apply(ctx, st, "job-2", ready("job-2", 2))
		first, err := st.Pending(bg, "board", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(st.MarkProcessed(bg, "board", first[0].Seq)).To(specs.BeNil())

		got, err := restart().Pending(bg, "board", []string{"StageEntered"})

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual(first[1:])
	})
}

// feedOps is a feed store a person can also clear dead events on.
type feedOps interface {
	feedStore
	ports.DeadEvents
}

// pendingSeqs saves a StageEntered for jobID and one for job-2 in st and
// returns their seqs, in that order.
func pendingSeqs(ctx *specs.Context, st feedStore) (ports.Seq, ports.Seq) {
	createJobs(ctx, st)
	apply(ctx, st, jobID, ready(jobID, 1))
	apply(ctx, st, "job-2", ready("job-2", 2))
	got, err := st.Pending(context.Background(), "board", []string{"StageEntered"})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(got).To(specs.HaveLen(2))
	return got[0].Seq, got[1].Seq
}

func failure(attempts int, next time.Time) ports.Failure {
	return ports.Failure{Attempts: attempts, LastError: "handler failed", NextAttempt: next}
}

func dead(attempts int) ports.Failure {
	return ports.Failure{Attempts: attempts, LastError: "handler failed", Dead: true}
}

// deliveryContract is how every EventFeed keeps failed deliveries and how a
// person clears a dead one.
func deliveryContract(s *specs.Spec, open func(ctx *specs.Context) (feedOps, func() feedOps)) {
	bg := context.Background()

	s.It("saves a failed delivery's attempts, last error and next attempt per subscriber and job, across a restart", func(ctx *specs.Context) {
		st, restart := open(ctx)
		first, second := pendingSeqs(ctx, st)

		ctx.Expect(st.MarkFailed(bg, "board", second, failure(1, at(10)))).To(specs.BeNil())
		ctx.Expect(st.MarkFailed(bg, "board", first, failure(1, at(10)))).To(specs.BeNil())
		ctx.Expect(st.MarkFailed(bg, "board", first, failure(2, at(12)))).To(specs.BeNil())

		st = restart()
		ds, err := st.Deliveries(bg, "board")
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(ds).ToEqual([]ports.Delivery{
			{Subscriber: "board", Seq: first, Job: jobID, Failure: failure(2, at(12))},
			{Subscriber: "board", Seq: second, Job: "job-2", Failure: failure(1, at(10))},
		})
		ds, err = st.Deliveries(bg, "inbox")
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(ds).To(specs.BeEmpty())
	})

	s.It("keeps a dead delivery with no next attempt, still pending, and clears a delivery once processed", func(ctx *specs.Context) {
		st, _ := open(ctx)
		first, second := pendingSeqs(ctx, st)
		ctx.Expect(st.MarkFailed(bg, "board", first, failure(5, at(10)))).To(specs.BeNil())
		ctx.Expect(st.MarkFailed(bg, "board", first, ports.Failure{Attempts: 6, LastError: "handler failed", NextAttempt: at(11), Dead: true})).To(specs.BeNil())
		ctx.Expect(st.MarkFailed(bg, "board", second, failure(1, at(10)))).To(specs.BeNil())
		ctx.Expect(st.MarkProcessed(bg, "board", second)).To(specs.BeNil())

		ds, err := st.Deliveries(bg, "board")
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(ds).ToEqual([]ports.Delivery{{Subscriber: "board", Seq: first, Job: jobID, Failure: dead(6)}})
		got, err := st.Pending(bg, "board", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(events(got)).ToEqual([]domain.Event{entered(jobID, domain.StageGate, 1)})
	})

	s.It("retries a dead event as pending with no failed attempts", func(ctx *specs.Context) {
		st, _ := open(ctx)
		first, _ := pendingSeqs(ctx, st)
		ctx.Expect(st.MarkFailed(bg, "board", first, dead(6))).To(specs.BeNil())

		ctx.Expect(st.RetryDead(bg, "board", first)).To(specs.BeNil())

		ds, err := st.Deliveries(bg, "board")
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(ds).To(specs.BeEmpty())
		got, err := st.Pending(bg, "board", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.HaveLen(2))
		ctx.Expect(st.RetryDead(bg, "board", first)).To(specs.MatchError(ports.ErrNotDead))
	})

	s.It("discards a dead event as processed, for its subscriber only", func(ctx *specs.Context) {
		st, _ := open(ctx)
		first, _ := pendingSeqs(ctx, st)
		ctx.Expect(st.MarkFailed(bg, "board", first, dead(6))).To(specs.BeNil())

		ctx.Expect(st.DiscardDead(bg, "board", first)).To(specs.BeNil())

		ds, err := st.Deliveries(bg, "board")
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(ds).To(specs.BeEmpty())
		got, err := st.Pending(bg, "board", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(events(got)).ToEqual([]domain.Event{entered("job-2", domain.StageGate, 2)})
		got, err = st.Pending(bg, "inbox", []string{"StageEntered"})
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).To(specs.HaveLen(2))
		ctx.Expect(st.DiscardDead(bg, "board", first)).To(specs.MatchError(ports.ErrNotDead))
	})

	s.It("refuses to retry or discard an event that is only retrying, or dead for another subscriber", func(ctx *specs.Context) {
		st, _ := open(ctx)
		first, second := pendingSeqs(ctx, st)
		ctx.Expect(st.MarkFailed(bg, "board", first, failure(1, at(10)))).To(specs.BeNil())
		ctx.Expect(st.MarkFailed(bg, "inbox", second, dead(6))).To(specs.BeNil())

		ctx.Expect(st.RetryDead(bg, "board", first)).To(specs.MatchError(ports.ErrNotDead))
		ctx.Expect(st.DiscardDead(bg, "board", first)).To(specs.MatchError(ports.ErrNotDead))
		ctx.Expect(st.RetryDead(bg, "board", second)).To(specs.MatchError(ports.ErrNotDead))
		ctx.Expect(st.DiscardDead(bg, "board", second)).To(specs.MatchError(ports.ErrNotDead))
	})

	s.It("refuses to mark an event that was never saved", func(ctx *specs.Context) {
		st, _ := open(ctx)
		_, second := pendingSeqs(ctx, st)

		ctx.Expect(st.MarkProcessed(bg, "board", second+1)).To(specs.MatchError(ports.ErrEventNotFound))
		ctx.Expect(st.MarkFailed(bg, "board", second+1, failure(1, at(10)))).To(specs.MatchError(ports.ErrEventNotFound))
	})
}

func TestDeliveries(t *testing.T) {
	specs.Describe(t, "sqlite.Store deliveries and dead events", func(s *specs.Spec) {
		deliveryContract(s, func(ctx *specs.Context) (feedOps, func() feedOps) {
			st, restart := sqliteRestartable(ctx)
			return st.(feedOps), func() feedOps { return restart().(feedOps) }
		})
	})
	specs.Describe(t, "memstore.Store (fake) deliveries and dead events", func(s *specs.Spec) {
		deliveryContract(s, func(ctx *specs.Context) (feedOps, func() feedOps) {
			st := newMemstore(ctx)
			return st, func() feedOps { return st }
		})
	})
}
