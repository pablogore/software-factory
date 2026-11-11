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

// stoppedCaps is issue #4 with its slices raised to 6, stopped past USD 8
// under its second wait.
func stoppedCaps() ports.IssueCaps {
	next := domain.NextSliceRequested{Job: "4-s6", Initiative: "4", At: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	return ports.IssueCaps{Issue: 4, Slices: 6, Waits: 2, Stop: &ports.CapStop{Next: next, Job: "4-s7", Cap: domain.CapUSD, Reached: 9.5, WaitSeq: 2}}
}

func TestCapLog(t *testing.T) {
	specs.Describe(t, "sqlite.Store cap log", func(s *specs.Spec) {
		capContract(s, func(ctx *specs.Context) ports.CapLog { return openStore(ctx) })

		s.It("keeps an issue's caps after the database is closed and opened again", func(ctx *specs.Context) {
			bg := context.Background()
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			st, err := sqlite.Open(bg, path)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(st.SaveIssueCaps(bg, stoppedCaps())).To(specs.BeNil())
			ctx.Expect(st.Close()).To(specs.BeNil())

			again, err := sqlite.Open(bg, path)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = again.Close() })
			got, err := again.IssueCaps(bg, 4)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(stoppedCaps())
		})
	})
	specs.Describe(t, "memstore.Store (fake) cap log", func(s *specs.Spec) {
		capContract(s, func(ctx *specs.Context) ports.CapLog { return newMemstore(ctx) })
	})
}

// capContract is how every store keeps each issue's caps: the SQLite store
// and the in-memory fake run it alike.
func capContract(s *specs.Spec, newLog func(ctx *specs.Context) ports.CapLog) {
	bg := context.Background()

	s.It("has an issue with nothing raised and not stopped before it is ever saved", func(ctx *specs.Context) {
		got, err := newLog(ctx).IssueCaps(bg, 4)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual(ports.IssueCaps{Issue: 4})
	})

	s.It("reads back what was saved last for each issue", func(ctx *specs.Context) {
		log := newLog(ctx)
		ctx.Expect(log.SaveIssueCaps(bg, stoppedCaps())).To(specs.BeNil())
		ctx.Expect(log.SaveIssueCaps(bg, ports.IssueCaps{Issue: 5, Waits: 1})).To(specs.BeNil())
		got, err := log.IssueCaps(bg, 4)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual(stoppedCaps())

		resumed := stoppedCaps()
		resumed.USD, resumed.Stop = 12, nil
		ctx.Expect(log.SaveIssueCaps(bg, resumed)).To(specs.BeNil())
		got, err = log.IssueCaps(bg, 4)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual(resumed)
		got, err = log.IssueCaps(bg, 5)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got).ToEqual(ports.IssueCaps{Issue: 5, Waits: 1})
	})

	s.It("does not share the saved stop with the caller", func(ctx *specs.Context) {
		log := newLog(ctx)
		c := stoppedCaps()
		ctx.Expect(log.SaveIssueCaps(bg, c)).To(specs.BeNil())
		c.Stop.WaitSeq = 9

		got, err := log.IssueCaps(bg, 4)

		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(got.Stop.WaitSeq).ToEqual(2)
	})
}
