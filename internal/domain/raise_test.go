package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestRaiseRefusal(t *testing.T) {
	specs.Describe(t, "RaiseRefusal", func(s *specs.Spec) {
		s.It("accepts every refusal", func(ctx *specs.Context) {
			ctx.Expect(domain.RaiseRefusals()).ToEqual([]domain.RaiseRefusal{domain.RaiseNoAuthor, domain.RaiseUnknownCap, domain.RaiseBadValue, domain.RaiseStaleWait, domain.RaiseNotStopped, domain.RaiseNotAbove})
			for _, r := range domain.RaiseRefusals() {
				ctx.Expect(r.Validate()).To(specs.BeNil())
			}
		})

		s.It("cannot be changed from outside the package", func(ctx *specs.Context) {
			refusals := domain.RaiseRefusals()
			refusals[0] = "sin quien"
			ctx.Expect(domain.RaiseRefusals()[0]).ToEqual(domain.RaiseNoAuthor)
		})

		s.It("rejects an unknown or missing refusal", func(ctx *specs.Context) {
			ctx.Expect(domain.RaiseRefusal("valor bajo").Validate()).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(domain.RaiseRefusal("").Validate()).To(specs.MatchError(domain.ErrMissingField))
		})

		s.It("travels in a wrapped error, code included", func(ctx *specs.Context) {
			err := fmt.Errorf("issue #1: %w", domain.RefuseRaise(domain.RaiseNotAbove, "issue-slices to %d, reached %d", 5, 5))

			ctx.Expect(errors.Is(err, domain.ErrRaiseRefused)).To(specs.BeTrue())
			code, ok := domain.RaiseRefusalOf(err)
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(code).ToEqual(domain.RaiseNotAbove)
			ctx.Expect(err.Error()).To(specs.Contain("raise-not-above: issue-slices to 5, reached 5"))
		})

		s.It("is not found in another error", func(ctx *specs.Context) {
			_, ok := domain.RaiseRefusalOf(domain.ErrInvalidValue)
			ctx.Expect(ok).To(specs.BeFalse())
		})
	})
}
