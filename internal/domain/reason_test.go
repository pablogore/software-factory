package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestNextReason(t *testing.T) {
	specs.Describe(t, "NextReason", func(s *specs.Spec) {
		s.It("accepts the five reasons", func(ctx *specs.Context) {
			ctx.Expect(domain.NextReasons()).ToEqual([]domain.NextReason{domain.NextTechLead, domain.NextPriority, domain.NextLowest, domain.NoNextEmpty, domain.NoNextHeld})
			for _, r := range domain.NextReasons() {
				ctx.Expect(r.Validate()).To(specs.BeNil())
			}
		})

		s.It("cannot be changed from outside the package", func(ctx *specs.Context) {
			reasons := domain.NextReasons()
			reasons[0] = "es el primero en el orden del tech lead"
			ctx.Expect(domain.NextReasons()[0]).ToEqual(domain.NextTechLead)
		})

		rows := []domain.NextReason{"es el primero en el orden del tech lead", "Tech-Lead-Order", "risk"}
		specs.Table(s, rows, func(r domain.NextReason) string { return "rejects " + string(r) }, func(ctx *specs.Context, r domain.NextReason) {
			ctx.Expect(r.Validate()).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("rejects a missing reason", func(ctx *specs.Context) {
			ctx.Expect(domain.NextReason("").Validate()).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
