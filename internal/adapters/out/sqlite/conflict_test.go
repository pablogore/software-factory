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
	winner := []domain.Event{domain.StageEntered{Job: jobID, Stage: domain.StageGate, At: at(1)}}
	loser := []domain.Event{domain.NeedsInfo{Job: jobID, From: "build", At: at(2)}}

	s.When("two writers load the same job and both save", func(s *specs.Spec) {
		s.It("the first saves and the second gets a version conflict", func(ctx *specs.Context) {
			st := newStore(ctx)
			a, va := created(ctx, st)
			b, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())

			_, err = st.Apply(bg, a, va, winner)
			ctx.Expect(err).To(specs.BeNil())
			_, err = st.Apply(bg, b, vb, loser)

			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))
		})

		s.It("the loser leaves the state, version and history as the winner left them", func(ctx *specs.Context) {
			st := newStore(ctx)
			a, va := created(ctx, st)
			b, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			won, err := st.Apply(bg, a, va, winner)
			ctx.Expect(err).To(specs.BeNil())

			_, err = st.Apply(bg, b, vb, loser)

			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))
			got, v, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(decide.Apply(a, winner))
			ctx.Expect(v).ToEqual(won)
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(winner[0], at(1))})
		})

		s.It("conflicts even when the winner's events leave the state unchanged", func(ctx *specs.Context) {
			st := newStore(ctx)
			a, va := created(ctx, st)
			b, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			same := []domain.Event{domain.GatePassed{Job: jobID, At: at(1)}}
			ctx.Expect(decide.Apply(a, same)).ToEqual(a)

			_, err = st.Apply(bg, a, va, same)
			ctx.Expect(err).To(specs.BeNil())
			_, err = st.Apply(bg, b, vb, same)

			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(same[0], at(1))})
		})

		s.It("the loser loads the job again and its next save succeeds", func(ctx *specs.Context) {
			st := newStore(ctx)
			a, va := created(ctx, st)
			b, vb, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			_, err = st.Apply(bg, a, va, winner)
			ctx.Expect(err).To(specs.BeNil())
			_, err = st.Apply(bg, b, vb, loser)
			ctx.Expect(err).To(specs.MatchError(ports.ErrVersionConflict))

			b, vb, err = st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			_, err = st.Apply(bg, b, vb, loser)

			ctx.Expect(err).To(specs.BeNil())
			got, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(decide.Apply(decide.Apply(a, winner), loser))
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(winner[0], at(1)), record(loser[0], at(2))})
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
				evs := [2][]domain.Event{
					{domain.StageEntered{Job: job.ID, Stage: domain.StageGate, At: at(1)}},
					{domain.NeedsInfo{Job: job.ID, From: "build", At: at(2)}},
				}
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
						_, errs[i] = st.Apply(bg, loaded[i], versions[i], evs[i])
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
