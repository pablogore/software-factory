package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestRunCap(t *testing.T) {
	specs.Describe(t, "RunCap", func(s *specs.Spec) {
		s.It("accepts the two caps", func(ctx *specs.Context) {
			ctx.Expect(domain.RunCaps()).ToEqual([]domain.RunCap{domain.CapRunIssues, domain.CapRunUSD})
			for _, c := range domain.RunCaps() {
				ctx.Expect(c.Validate()).To(specs.BeNil())
			}
		})

		s.It("cannot be changed from outside the package", func(ctx *specs.Context) {
			caps := domain.RunCaps()
			caps[0] = "llegó a 5 issues"
			ctx.Expect(domain.RunCaps()[0]).ToEqual(domain.CapRunIssues)
		})

		rows := []domain.RunCap{"llegó a 5 issues", "Run-USD", "issue-usd"}
		specs.Table(s, rows, func(c domain.RunCap) string { return "rejects " + string(c) }, func(ctx *specs.Context, c domain.RunCap) {
			ctx.Expect(c.Validate()).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("rejects a missing cap", func(ctx *specs.Context) {
			ctx.Expect(domain.RunCap("").Validate()).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
