package portrange_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/portrange"
)

func newAllocator(ctx *specs.Context, base, step int) *portrange.Allocator {
	ctx.Testing().Helper()
	a, err := portrange.New(base, step)
	ctx.Expect(err).To(specs.BeNil())
	return a
}

func acquire(ctx *specs.Context, a *portrange.Allocator, job domain.JobID) portrange.Range {
	ctx.Testing().Helper()
	r, err := a.Acquire(job)
	ctx.Expect(err).To(specs.BeNil())
	return r
}

type configRow struct {
	base, step int
}

func TestPortRange(t *testing.T) {
	specs.Describe(t, "portrange.Allocator", func(s *specs.Spec) {
		s.It("gives two jobs open at once distinct ranges that don't overlap, at base + step × n", func(ctx *specs.Context) {
			a := newAllocator(ctx, portrange.DefaultBase, portrange.DefaultStep)

			first := acquire(ctx, a, "job-1")
			second := acquire(ctx, a, "job-2")

			ctx.Expect(first).ToEqual(portrange.Range{First: 5100, Last: 5109})
			ctx.Expect(second).ToEqual(portrange.Range{First: 5110, Last: 5119})
		})

		s.It("gives a job that asks again the range it already holds", func(ctx *specs.Context) {
			a := newAllocator(ctx, 5100, 10)
			first := acquire(ctx, a, "job-1")
			acquire(ctx, a, "job-2")

			ctx.Expect(acquire(ctx, a, "job-1")).ToEqual(first)
			ctx.Expect(acquire(ctx, a, "job-3")).ToEqual(portrange.Range{First: 5120, Last: 5129})
		})

		s.It("hands a released range to the next job that asks, the smallest n first", func(ctx *specs.Context) {
			a := newAllocator(ctx, 5100, 10)
			acquire(ctx, a, "job-1")
			freed := acquire(ctx, a, "job-2")
			acquire(ctx, a, "job-3")

			a.Release("job-2")

			ctx.Expect(acquire(ctx, a, "job-4")).ToEqual(freed)
			ctx.Expect(acquire(ctx, a, "job-5")).ToEqual(portrange.Range{First: 5130, Last: 5139})
		})

		s.It("leaves other jobs' ranges alone when releasing a job that holds none, or twice", func(ctx *specs.Context) {
			a := newAllocator(ctx, 5100, 10)
			held := acquire(ctx, a, "job-1")
			acquire(ctx, a, "job-2")

			a.Release("job-2")
			a.Release("job-2")
			a.Release("job-unknown")

			ctx.Expect(acquire(ctx, a, "job-1")).ToEqual(held)
			ctx.Expect(acquire(ctx, a, "job-3")).ToEqual(portrange.Range{First: 5110, Last: 5119})
		})

		s.It("fails with ErrNoFreeRange instead of repeating ports when every range that fits is held", func(ctx *specs.Context) {
			// 65500..65529 holds three whole ranges; 65530..65539 would pass 65535.
			a := newAllocator(ctx, 65500, 10)
			acquire(ctx, a, "job-1")
			acquire(ctx, a, "job-2")
			acquire(ctx, a, "job-3")

			_, err := a.Acquire("job-4")

			ctx.Expect(err).To(specs.MatchError(portrange.ErrNoFreeRange))
		})

		s.It("fails with ErrNoFreeRange when not even one range fits under the highest port", func(ctx *specs.Context) {
			a := newAllocator(ctx, 65530, 10)

			_, err := a.Acquire("job-1")

			ctx.Expect(err).To(specs.MatchError(portrange.ErrNoFreeRange))
		})

		s.It("refuses a job with no id", func(ctx *specs.Context) {
			a := newAllocator(ctx, 5100, 10)

			_, err := a.Acquire("")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})

		rows := []configRow{{0, 10}, {-1, 10}, {65536, 10}, {5100, 0}, {5100, -10}, {5100, 65536}}
		specs.Table(s, rows, func(r configRow) string {
			return fmt.Sprintf("refuses base %d with step %d", r.base, r.step)
		}, func(ctx *specs.Context, r configRow) {
			_, err := portrange.New(r.base, r.step)
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("never gives two jobs asking at once the same range", func(ctx *specs.Context) {
			a := newAllocator(ctx, 5100, 10)
			const jobs = 50
			got := make([]portrange.Range, jobs)
			errs := make([]error, jobs)
			var wg sync.WaitGroup
			for i := range jobs {
				wg.Go(func() { got[i], errs[i] = a.Acquire(domain.JobID(fmt.Sprintf("job-%d", i))) })
			}
			wg.Wait()

			seen := map[int]bool{}
			for i := range jobs {
				ctx.Expect(errs[i]).To(specs.BeNil())
				ctx.Expect(seen[got[i].First]).To(specs.BeFalse())
				seen[got[i].First] = true
			}
			ctx.Expect(seen).To(specs.HaveLen(jobs))
		})
	})
}
