package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/ports"
)

// conflictContract is how every JobStore treats two writers of one job: the
// first save wins and the late one gets ErrVersionConflict.
func conflictContract(s *specs.Spec, newStore func(ctx *specs.Context) store) {
	bg := context.Background()
	winner, loser := built(domain.BuildReady, 1), built(domain.BuildBlocked, 2)

	s.When("two writers load the same job and both save", func(s *specs.Spec) {
		s.It("the first saves and the second gets a version conflict", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, va := created(ctx, st)
			_, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = st.Apply(bg, jobID, va, pipeline(), winner)
			ctx.Expect(err).To(specs.BeNil())
			_, _, err = st.Apply(bg, jobID, vb, pipeline(), loser)

			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))
		})

		s.It("the loser leaves the state, version and history as the winner left them", func(ctx *specs.Context) {
			st := newStore(ctx)
			a, va := created(ctx, st)
			_, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			evs, won, err := st.Apply(bg, jobID, va, pipeline(), winner)
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = st.Apply(bg, jobID, vb, pipeline(), loser)

			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))
			got, v, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(decide.Apply(a, evs))
			ctx.Expect(v).ToEqual(won)
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(evs[0], at(1))})
		})

		s.It("conflicts even when the loser applies the same event as the winner", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, va := created(ctx, st)
			_, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())

			evs, _, err := st.Apply(bg, jobID, va, pipeline(), winner)
			ctx.Expect(err).To(specs.BeNil())
			_, _, err = st.Apply(bg, jobID, vb, pipeline(), winner)

			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(evs[0], at(1))})
		})

		s.It("the loser loads the job again and its next save is decided on the winner's state", func(ctx *specs.Context) {
			st := newStore(ctx)
			a, va := created(ctx, st)
			_, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			first, _, err := st.Apply(bg, jobID, va, pipeline(), winner)
			ctx.Expect(err).To(specs.BeNil())
			_, _, err = st.Apply(bg, jobID, vb, pipeline(), loser)
			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))

			b, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			gate := domain.GateFailed{Job: jobID, At: at(2)}
			second, _, err := st.Apply(bg, jobID, vb, pipeline(), gate)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(second).ToEqual(decided(ctx, b, gate))
			got, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(decide.Apply(decide.Apply(a, first), second))
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			want := []ports.Record{record(first[0], at(1))}
			for _, ev := range second {
				want = append(want, record(ev, at(2)))
			}
			ctx.Expect(hist).ToEqual(want)
		})
	})
}

func TestStoreConflict(t *testing.T) {
	specs.Describe(t, "sqlite.Store version conflict", func(s *specs.Spec) {
		conflictContract(s, func(ctx *specs.Context) store {
			st, err := sqlite.Open(context.Background(), filepath.Join(ctx.Testing().TempDir(), "factory.db"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = st.Close() })
			return st
		})
	})
}

func TestMemstoreConflict(t *testing.T) {
	specs.Describe(t, "memstore.Store (fake) version conflict", func(s *specs.Spec) {
		conflictContract(s, func(*specs.Context) store { return memstore.New() })
	})
}

// TestStoreConflictAcrossStores stands in for two processes: two stores on
// one file share nothing but the database, so only SQLite orders them.
func TestStoreConflictAcrossStores(t *testing.T) {
	specs.Describe(t, "two sqlite.Stores on one file", func(s *specs.Spec) {
		bg := context.Background()

		s.It("saving the same job at the same time, exactly one wins and the other gets a version conflict, with no lock files", func(ctx *specs.Context) {
			dir := ctx.Testing().TempDir()
			path := filepath.Join(dir, "factory.db")
			stores := [2]*sqlite.Store{}
			for i := range stores {
				st, err := sqlite.Open(bg, path)
				ctx.Expect(err).To(specs.BeNil())
				stores[i] = st
			}

			for round := range 50 {
				job := newJob()
				job.ID = domain.JobID(fmt.Sprintf("job-%d", round))
				ctx.Expect(stores[0].Create(bg, job)).To(specs.BeNil())
				triggers := [2]domain.Event{
					domain.ResultSubmitted{Job: job.ID, Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at(1)},
					domain.ResultSubmitted{Job: job.ID, Role: "builder", Result: domain.BuildResult{Status: domain.BuildBlocked}, At: at(2)},
				}
				var evs [2][]domain.Event
				var loaded [2]domain.Job
				var versions [2]ports.Version
				for i, st := range stores {
					j, v, err := st.Load(bg, job.ID)
					ctx.Expect(err).To(specs.BeNil())
					loaded[i], versions[i] = j, v
				}

				var errs [2]error
				var start, done sync.WaitGroup
				start.Add(1)
				for i, st := range stores {
					done.Go(func() {
						start.Wait()
						evs[i], _, errs[i] = st.Apply(bg, job.ID, versions[i], pipeline(), triggers[i])
					})
				}
				start.Done()
				done.Wait()

				won := 0
				if errs[1] == nil {
					won = 1
				}
				ctx.Expect(errs[won]).To(specs.BeNil())
				ctx.Expect(errors.Is(errs[1-won], ports.ErrVersionConflict)).To(specs.BeTrue())
				got, _, err := stores[1-won].Load(bg, job.ID)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(got).ToEqual(decide.Apply(loaded[won], evs[won]))
				hist, err := stores[1-won].History(bg, job.ID)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(hist).ToEqual([]ports.Record{record(evs[won][0], at(won+1))})
			}

			for _, st := range stores {
				ctx.Expect(st.Close()).To(specs.BeNil())
			}
			entries, err := os.ReadDir(dir)
			ctx.Expect(err).To(specs.BeNil())
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			ctx.Expect(names).ToEqual([]string{"factory.db"})
		})
	})
}
