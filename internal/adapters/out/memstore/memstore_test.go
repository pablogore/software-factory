package memstore_test

import (
	"context"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
)

type invalidRow struct {
	name     string
	edit     func(*domain.Job)
	sentinel error
	field    string
}

// The shared contract runs in internal/adapters/out/sqlite. These cases need
// a job saved broken, and only this package's tests can plant one.
func TestMemstorePlanted(t *testing.T) {
	specs.Describe(t, "memstore.Store (fake) with a saved job breaking an invariant", func(s *specs.Spec) {
		bg := context.Background()
		const id domain.JobID = "job-1"
		rows := []invalidRow{
			{"a wait stage with wait_seq 0", func(j *domain.Job) { j.Stage, j.WaitSeq = domain.StageNeedsInfo, 0 }, domain.ErrInvalidValue, "wait_seq"},
			{"needs-human without a cause", func(j *domain.Job) { j.Stage, j.WaitSeq = domain.StageNeedsHuman, 1 }, domain.ErrMissingField, "cause"},
			{"an unknown cause", func(j *domain.Job) { j.Stage, j.WaitSeq, j.Cause = domain.StageNeedsHuman, 1, "bored" }, domain.ErrInvalidValue, "cause"},
		}
		planted := func(ctx *specs.Context, edit func(*domain.Job)) *memstore.Store {
			st, err := memstore.New()
			ctx.Expect(err).To(specs.BeNil())
			job := domain.Job{ID: id, Pipeline: "default", Stage: "build", Round: 1}
			ctx.Expect(st.Create(bg, job)).To(specs.BeNil())
			edit(&job)
			ctx.Expect(st.Plant(bg, job)).To(specs.BeNil())
			return st
		}

		specs.Table(s, rows, func(r invalidRow) string {
			return "refuses to load it with " + r.name + ", naming the field"
		}, func(ctx *specs.Context, r invalidRow) {
			st := planted(ctx, r.edit)

			_, _, err := st.Load(bg, id)

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			ctx.Expect(err.Error()).To(specs.Contain(r.field))
		})

		specs.Table(s, rows, func(r invalidRow) string {
			return "refuses to apply an event to it with " + r.name + ", saving nothing"
		}, func(ctx *specs.Context, r invalidRow) {
			st := planted(ctx, r.edit)
			p := domain.Pipeline{Name: "default", Stages: []domain.Stage{{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}}}}

			_, _, err := st.Apply(bg, id, 1, p, domain.StopRequested{Job: id, By: "ana"})

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			ctx.Expect(err.Error()).To(specs.Contain(r.field))
			hist, err := st.History(bg, id)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
		})
	})
}
