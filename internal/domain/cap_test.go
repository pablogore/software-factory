package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestIssueCap(t *testing.T) {
	specs.Describe(t, "IssueCap", func(s *specs.Spec) {
		s.It("accepts the two caps", func(ctx *specs.Context) {
			ctx.Expect(domain.IssueCaps()).ToEqual([]domain.IssueCap{domain.CapSlices, domain.CapUSD})
			for _, c := range domain.IssueCaps() {
				ctx.Expect(c.Validate()).To(specs.BeNil())
			}
		})

		s.It("cannot be changed from outside the package", func(ctx *specs.Context) {
			caps := domain.IssueCaps()
			caps[0] = "pasó de 5 slices"
			ctx.Expect(domain.IssueCaps()[0]).ToEqual(domain.CapSlices)
		})

		rows := []domain.IssueCap{"pasó de 5 slices", "Issue-USD", "run-usd"}
		specs.Table(s, rows, func(c domain.IssueCap) string { return "rejects " + string(c) }, func(ctx *specs.Context, c domain.IssueCap) {
			ctx.Expect(c.Validate()).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("rejects a missing cap", func(ctx *specs.Context) {
			ctx.Expect(domain.IssueCap("").Validate()).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
