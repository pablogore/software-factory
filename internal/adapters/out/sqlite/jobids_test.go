package sqlite_test

import (
	"context"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestJobIDs(t *testing.T) {
	specs.Describe(t, "sqlite.Store JobIDs", func(s *specs.Spec) {
		bg := context.Background()

		s.It("lists none in a new store", func(ctx *specs.Context) {
			ids, err := openStore(ctx).JobIDs(bg)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(ids).To(specs.BeEmpty())
		})

		s.It("lists every saved job's ID in order", func(ctx *specs.Context) {
			st := openStore(ctx)
			for _, id := range []domain.JobID{"j-2", "j-1", "j-3"} {
				job := newJob()
				job.ID = id
				ctx.Expect(st.Create(bg, job)).To(specs.BeNil())
			}

			ids, err := st.JobIDs(bg)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(ids).ToEqual([]domain.JobID{"j-1", "j-2", "j-3"})
		})
	})
}
