package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestHumanCause(t *testing.T) {
	specs.Describe(t, "HumanCause", func(s *specs.Spec) {
		s.It("accepts the six causes", func(ctx *specs.Context) {
			for _, c := range domain.HumanCauses {
				ctx.Expect(c.Validate()).To(specs.BeNil())
			}
			ctx.Expect(domain.HumanCauses).To(specs.HaveLen(6))
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
