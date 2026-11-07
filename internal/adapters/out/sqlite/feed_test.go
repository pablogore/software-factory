package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

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
