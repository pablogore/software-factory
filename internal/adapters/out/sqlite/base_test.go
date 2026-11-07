package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/ports"
)

const (
	commitA = "1111111111111111111111111111111111111111"
	commitB = "2222222222222222222222222222222222222222"
)

func TestBaseLog(t *testing.T) {
	specs.Describe(t, "sqlite.Store base log", func(s *specs.Spec) {
		baseContract(s, func(ctx *specs.Context) ports.BaseLog { return openStore(ctx) })

		s.It("keeps a job's base after the database is closed and opened again", func(ctx *specs.Context) {
			bg := context.Background()
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			st, err := sqlite.Open(bg, path)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(st.RecordBase(bg, "j-1", commitA)).To(specs.BeNil())
			ctx.Expect(st.Close()).To(specs.BeNil())

			again, err := sqlite.Open(bg, path)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = again.Close() })
			got, err := again.Base(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(commitA)
		})
	})
	specs.Describe(t, "memstore.Store (fake) base log", func(s *specs.Spec) {
		baseContract(s, func(ctx *specs.Context) ports.BaseLog { return newMemstore(ctx) })
	})
}

// baseContract is how every store keeps the commit a job's worktree was
// made from: the SQLite store and the in-memory fake run it alike.
func baseContract(s *specs.Spec, newLog func(ctx *specs.Context) ports.BaseLog) {
	bg := context.Background()

	s.It("has no base for a job before one is recorded", func(ctx *specs.Context) {
		_, err := newLog(ctx).Base(bg, "j-1")

		ctx.Expect(err).To(specs.MatchError(ports.ErrNoBase))
	})

	s.It("reads back the base recorded for each job", func(ctx *specs.Context) {
		log := newLog(ctx)
		ctx.Expect(log.RecordBase(bg, "j-1", commitA)).To(specs.BeNil())
		ctx.Expect(log.RecordBase(bg, "j-2", commitB)).To(specs.BeNil())

		got1, err1 := log.Base(bg, "j-1")
		got2, err2 := log.Base(bg, "j-2")

		ctx.Expect(err1).To(specs.BeNil())
		ctx.Expect(got1).ToEqual(commitA)
		ctx.Expect(err2).To(specs.BeNil())
		ctx.Expect(got2).ToEqual(commitB)
	})

	s.It("accepts the same base recorded again", func(ctx *specs.Context) {
		log := newLog(ctx)
		ctx.Expect(log.RecordBase(bg, "j-1", commitA)).To(specs.BeNil())

		ctx.Expect(log.RecordBase(bg, "j-1", commitA)).To(specs.BeNil())
	})

	s.It("refuses a different base for a job that has one, keeping the first", func(ctx *specs.Context) {
		log := newLog(ctx)
		ctx.Expect(log.RecordBase(bg, "j-1", commitA)).To(specs.BeNil())

		err := log.RecordBase(bg, "j-1", commitB)

		ctx.Expect(err).To(specs.MatchError(ports.ErrBaseRecorded))
		got, err := log.Base(bg, "j-1")
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual(commitA)
	})
}
