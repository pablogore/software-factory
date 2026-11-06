package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

type modeRow struct {
	mode domain.Mode
	want string
}

func TestRole(t *testing.T) {
	specs.Describe(t, "Role.Validate", func(s *specs.Spec) {
		rows := []modeRow{
			{domain.ModeShadow, ""},
			{domain.ModeAdvisory, ""},
			{domain.ModeBlocking, ""},
			{"loud", `role security-deep: invalid value: mode "loud"`},
			{"", `role security-deep: invalid value: mode ""`},
		}
		specs.Table(s, rows, func(r modeRow) string {
			return "checks mode " + string(r.mode)
		}, func(ctx *specs.Context, r modeRow) {
			role := domain.Role{Name: "security-deep", Kind: domain.KindReview, Prompt: "roles/security-deep.md", Mode: r.mode}

			err := role.Validate()

			if r.want == "" {
				ctx.Expect(err).To(specs.BeNil())
				return
			}
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(r.want)
		})
	})
}
