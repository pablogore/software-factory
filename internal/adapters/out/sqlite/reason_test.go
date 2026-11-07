package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/eventjson"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// reasonContract is how every JobStore keeps an override's reason: cut to
// the maximum whoever opened it set, else eventjson.DefaultMaxReason, never
// mid-character, and otherwise word for word, saved with its merge.
// openWith opens a store with that maximum.
func reasonContract(s *specs.Spec, newStore func(ctx *specs.Context) store, openWith func(ctx *specs.Context, maxReason int) (store, error)) {
	bg := context.Background()

	// overridden applies an override with reason to a job waiting for one and
	// returns what Apply returned and the history it left.
	overridden := func(ctx *specs.Context, st store, reason string) ([]domain.Event, []ports.Record) {
		_, v := createdWaiting(ctx, st)
		override := domain.OverrideRequested{Job: jobID, By: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Reason: reason, WaitSeq: 1, At: at(1)}
		evs, _, err := st.Apply(bg, jobID, v, pipeline(), override)
		ctx.Expect(err).To(specs.BeNil())
		hist, err := st.History(bg, jobID)
		ctx.Expect(err).To(specs.BeNil())
		return evs, hist
	}
	saved := func(reason string) []ports.Record {
		return []ports.Record{
			record(domain.MergeOverridden{Job: jobID, By: "ana", Reason: reason, At: at(1)}, at(1)),
			record(domain.MergeRequested{Job: jobID, At: at(1)}, at(1)),
		}
	}

	s.When("no maximum is set", func(s *specs.Spec) {
		s.It("saves a 10,000-character reason cut to the default maximum, then its merge request", func(ctx *specs.Context) {
			long := strings.Repeat("why ", 2500)

			evs, hist := overridden(ctx, newStore(ctx), long)

			want := saved(long[:eventjson.DefaultMaxReason])
			ctx.Expect(hist).ToEqual(want)
			ctx.Expect(evs).ToEqual([]domain.Event{want[0].Event, want[1].Event})
		})

		s.It("reads a shorter reason back exactly as written, newlines included", func(ctx *specs.Context) {
			short := "ship it:\nQA signed off\r\n\tthe flaky test is #31  "

			evs, hist := overridden(ctx, newStore(ctx), short)

			want := saved(short)
			ctx.Expect(hist).ToEqual(want)
			ctx.Expect(evs).ToEqual([]domain.Event{want[0].Event, want[1].Event})
		})
	})

	s.When("whoever opens the store sets a maximum", func(s *specs.Spec) {
		s.It("cuts a longer reason to that many characters, never mid-character", func(ctx *specs.Context) {
			st, err := openWith(ctx, 3)
			ctx.Expect(err).To(specs.BeNil())

			_, hist := overridden(ctx, st, "h\u00e9\u00e9 w\u00f6rld")

			ctx.Expect(hist).ToEqual(saved("h\u00e9\u00e9"))
			reason := hist[0].Event.(domain.MergeOverridden).Reason
			ctx.Expect(utf8.ValidString(reason)).To(specs.BeTrue())
		})

		s.It("keeps a reason of exactly that many characters whole", func(ctx *specs.Context) {
			st, err := openWith(ctx, 4)
			ctx.Expect(err).To(specs.BeNil())

			_, hist := overridden(ctx, st, "日本語!")

			ctx.Expect(hist).ToEqual(saved("日本語!"))
		})

		s.It("refuses to open with a maximum below 1", func(ctx *specs.Context) {
			_, err := openWith(ctx, 0)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}

func TestStoreReason(t *testing.T) {
	specs.Describe(t, "sqlite.Store override reason", func(s *specs.Spec) {
		reasonContract(s, func(ctx *specs.Context) store { return openStore(ctx) },
			func(ctx *specs.Context, n int) (store, error) {
				st, err := sqlite.Open(context.Background(), filepath.Join(ctx.Testing().TempDir(), "factory.db"), sqlite.WithMaxReason(n))
				if err != nil {
					return nil, err
				}
				ctx.Cleanup(func() { _ = st.Close() })
				return st, nil
			})
	})
}

func TestMemstoreReason(t *testing.T) {
	specs.Describe(t, "memstore.Store (fake) override reason", func(s *specs.Spec) {
		reasonContract(s, func(ctx *specs.Context) store { return newMemstore(ctx) },
			func(_ *specs.Context, n int) (store, error) {
				st, err := memstore.New(memstore.WithMaxReason(n))
				if err != nil {
					return nil, err
				}
				return st, nil
			})
	})
}
