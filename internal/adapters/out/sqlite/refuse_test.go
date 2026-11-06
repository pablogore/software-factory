package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/ports"
)

type invalidRow struct {
	name     string
	edit     func(*domain.Job)
	sentinel error
	field    string
}

// invalidJobs break each invariant domain.Job.Validate checks.
func invalidJobs() []invalidRow {
	return []invalidRow{
		{"a wait stage with wait_seq 0", func(j *domain.Job) { j.Stage, j.WaitSeq = domain.StageNeedsInfo, 0 }, domain.ErrInvalidValue, "wait_seq"},
		{"needs-human without a cause", func(j *domain.Job) { j.Stage, j.WaitSeq = domain.StageNeedsHuman, 1 }, domain.ErrMissingField, "cause"},
		{"an unknown cause", func(j *domain.Job) { j.Stage, j.WaitSeq, j.Cause = domain.StageNeedsHuman, 1, "bored" }, domain.ErrInvalidValue, "cause"},
	}
}

// refuseContract is what every JobStore refuses to save or load: an event
// Decide did not return, and a job breaking an invariant.
func refuseContract(s *specs.Spec, newStore func(ctx *specs.Context) store) {
	bg := context.Background()
	stop := domain.StopRequested{Job: jobID, By: "ana", At: at(1)}
	stopped := domain.JobStopped{Job: jobID, By: "ana", At: at(1)}

	s.When("the job is stopped", func(s *specs.Spec) {
		rows := []sample{
			{"a forged StageEntered", domain.StageEntered{Job: jobID, Stage: "build", At: at(2)}},
			{"a forged MergeRequested", domain.MergeRequested{Job: jobID, At: at(2)}},
			{"a builder result", built(domain.BuildReady, 2)},
			{"a person's message", domain.MessageReceived{Job: jobID, Provider: "github", Thread: "29", Author: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Text: "go on", WaitSeq: 1, At: at(2)}},
		}
		specs.Table(s, rows, func(r sample) string {
			return "refuses " + r.name + " Decide did not return, saving neither the state nor any event"
		}, func(ctx *specs.Context, r sample) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			_, v, err := st.Apply(bg, jobID, v, pipeline(), stop)
			ctx.Expect(err).To(specs.BeNil())
			before, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(before.Stage).ToEqual(domain.StageStopped)

			evs, _, err := st.Apply(bg, jobID, v, pipeline(), r.ev)

			ctx.Expect(err).To(specs.MatchError(decide.ErrStopped))
			ctx.Expect(evs).To(specs.BeEmpty())
			got, gotV, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(before)
			ctx.Expect(gotV).ToEqual(v)
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(stopped, at(1))})
		})
	})

	s.When("a job breaks an invariant", func(s *specs.Spec) {
		specs.Table(s, invalidJobs(), func(r invalidRow) string {
			return "refuses to create a job with " + r.name + ", naming the field, and saves nothing"
		}, func(ctx *specs.Context, r invalidRow) {
			st := newStore(ctx)
			job := newJob()
			r.edit(&job)

			err := st.Create(bg, job)

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			ctx.Expect(err.Error()).To(specs.Contain(r.field))
			_, _, err = st.Load(bg, jobID)
			ctx.Expect(err).To(specs.MatchError(ports.ErrJobNotFound))
		})

		specs.Table(s, invalidJobs(), func(r invalidRow) string {
			return "refuses to load a saved job with " + r.name + ", naming the field"
		}, func(ctx *specs.Context, r invalidRow) {
			st := newStore(ctx)
			job := newJob()
			r.edit(&job)
			ctx.Expect(st.Plant(bg, job)).To(specs.BeNil())

			_, _, err := st.Load(bg, jobID)

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			ctx.Expect(err.Error()).To(specs.Contain(r.field))
		})

		specs.Table(s, invalidJobs(), func(r invalidRow) string {
			return "refuses to apply an event to a saved job with " + r.name + ", saving nothing"
		}, func(ctx *specs.Context, r invalidRow) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			job := newJob()
			r.edit(&job)
			ctx.Expect(st.Plant(bg, job)).To(specs.BeNil())

			_, _, err := st.Apply(bg, jobID, v, pipeline(), stop)

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			ctx.Expect(err.Error()).To(specs.Contain(r.field))
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
		})
	})
}

func TestStoreRefuses(t *testing.T) {
	specs.Describe(t, "sqlite.Store refuses", func(s *specs.Spec) {
		refuseContract(s, func(ctx *specs.Context) store {
			st, err := sqlite.Open(context.Background(), filepath.Join(ctx.Testing().TempDir(), "factory.db"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = st.Close() })
			return st
		})
	})
}

func TestMemstoreRefuses(t *testing.T) {
	specs.Describe(t, "memstore.Store (fake) refuses", func(s *specs.Spec) {
		refuseContract(s, func(*specs.Context) store { return memstore.New() })
	})
}
