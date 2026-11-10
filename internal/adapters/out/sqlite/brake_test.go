package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/ports"
)

func TestBrakeLog(t *testing.T) {
	specs.Describe(t, "sqlite.Store brake log", func(s *specs.Spec) {
		brakeContract(s, func(ctx *specs.Context) ports.BrakeLog { return openStore(ctx) })

		s.It("keeps the brake on after the database is closed and opened again", func(ctx *specs.Context) {
			bg := context.Background()
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			st, err := sqlite.Open(bg, path)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(st.SaveBrake(bg, true)).To(specs.BeNil())
			ctx.Expect(st.Close()).To(specs.BeNil())

			again, err := sqlite.Open(bg, path)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = again.Close() })
			on, err := again.BrakeOn(bg)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(on).To(specs.BeTrue())
		})
	})
	specs.Describe(t, "memstore.Store (fake) brake log", func(s *specs.Spec) {
		brakeContract(s, func(ctx *specs.Context) ports.BrakeLog { return newMemstore(ctx) })
	})
}

// brakeContract is how every store keeps the run's brake: the SQLite store
// and the in-memory fake run it alike.
func brakeContract(s *specs.Spec, newLog func(ctx *specs.Context) ports.BrakeLog) {
	bg := context.Background()

	s.It("has the brake off before it is ever saved", func(ctx *specs.Context) {
		on, err := newLog(ctx).BrakeOn(bg)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(on).To(specs.BeFalse())
	})

	s.It("reads back what was saved last", func(ctx *specs.Context) {
		log := newLog(ctx)
		ctx.Expect(log.SaveBrake(bg, true)).To(specs.BeNil())
		on, err := log.BrakeOn(bg)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(on).To(specs.BeTrue())

		ctx.Expect(log.SaveBrake(bg, false)).To(specs.BeNil())
		on, err = log.BrakeOn(bg)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(on).To(specs.BeFalse())
	})
}
