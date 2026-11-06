package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestHumanCause(t *testing.T) {
	specs.Describe(t, "HumanCause", func(s *specs.Spec) {
		s.It("accepts the six causes", func(ctx *specs.Context) {
			for _, c := range domain.HumanCauses() {
				ctx.Expect(c.Validate()).To(specs.BeNil())
			}
			ctx.Expect(domain.HumanCauses()).To(specs.HaveLen(6))
		})

		s.It("cannot be changed from outside the package", func(ctx *specs.Context) {
			causes := domain.HumanCauses()
			causes[0] = "approved"
			_ = append(causes[:1], "merge-anyway")

			ctx.Expect(domain.HumanCauses()).ToEqual([]domain.HumanCause{domain.CauseBlocked, domain.CauseEscalated, domain.CauseChanges,
				domain.CauseGateFailed, domain.CauseSpecEscalated, domain.CauseSpecException})
			ctx.Expect(domain.HumanCause("approved").Validate()).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(domain.HumanCause("merge-anyway").Validate()).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(domain.CauseBlocked.Validate()).To(specs.BeNil())
		})

		rows := []domain.HumanCause{"approved", "Blocked", "gate failed in the last round (3 of 3)"}
		specs.Table(s, rows, func(c domain.HumanCause) string { return "rejects " + string(c) }, func(ctx *specs.Context, c domain.HumanCause) {
			ctx.Expect(c.Validate()).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("rejects a missing cause", func(ctx *specs.Context) {
			ctx.Expect(domain.HumanCause("").Validate()).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
