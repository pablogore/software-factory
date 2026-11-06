package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

type modeRow struct {
	mode domain.Mode
	want string
}

type roleNameRow struct {
	name string
	role string
	want string
}

func TestRoleName(t *testing.T) {
	specs.Describe(t, "ValidateRoleName", func(s *specs.Spec) {
		s.It("accepts every default role name", func(ctx *specs.Context) {
			for _, k := range domain.Kinds {
				if name, ok := domain.DefaultRole(k); ok {
					ctx.Expect(domain.ValidateRoleName(name)).To(specs.BeNil())
				}
			}
		})

		rows := []roleNameRow{
			{"accepts lowercase letters, digits and hyphens", "reviewer-code2", ""},
			{"accepts a one-letter name", "a", ""},
			{"accepts a name of 64 characters", strings.Repeat("a", 64), ""},
			{"rejects an uppercase letter", "Builder", `invalid value: role name "Builder", want only lowercase letters, digits and hyphens`},
			{"rejects an underscore", "builder_x", `invalid value: role name "builder_x", want only lowercase letters, digits and hyphens`},
			{"rejects a trailing space", "builder ", `invalid value: role name "builder ", want only lowercase letters, digits and hyphens`},
			{"rejects a trailing tab", "builder\t", `invalid value: role name "builder\t", want only lowercase letters, digits and hyphens`},
			{"rejects a space inside", "reviewer code", `invalid value: role name "reviewer code", want only lowercase letters, digits and hyphens`},
			{"rejects a Cyrillic look-alike, escaped in the message", "b\u0443ilder", `invalid value: role name "b\u0443ilder", want only lowercase letters, digits and hyphens`},
			{"rejects a non-breaking hyphen look-alike", "reviewer\u2011code", `invalid value: role name "reviewer\u2011code", want only lowercase letters, digits and hyphens`},
			{"rejects a fullwidth letter", "\uff42uilder", `invalid value: role name "\uff42uilder", want only lowercase letters, digits and hyphens`},
			{"rejects invalid UTF-8", "builder\xff", `invalid value: role name "builder\xff", want only lowercase letters, digits and hyphens`},
			{"rejects a dot", "builder.go", `invalid value: role name "builder.go", want only lowercase letters, digits and hyphens`},
			{"rejects an empty name", "", "invalid value: role name is empty"},
			{"rejects a hyphen at the start", "-builder", `invalid value: role name "-builder" starts or ends with a hyphen`},
			{"rejects a hyphen at the end", "builder-", `invalid value: role name "builder-" starts or ends with a hyphen`},
			{"rejects a lone hyphen", "-", `invalid value: role name "-" starts or ends with a hyphen`},
			{"rejects a name of 65 characters", strings.Repeat("a", 65), `invalid value: role name "` + strings.Repeat("a", 65) + `" is 65 characters, want at most 64`},
		}
		specs.Table(s, rows, func(r roleNameRow) string { return r.name }, func(ctx *specs.Context, r roleNameRow) {
			err := domain.ValidateRoleName(r.role)

			if r.want == "" {
				ctx.Expect(err).To(specs.BeNil())
				return
			}
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(r.want)
		})
	})
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

	specs.Describe(t, "Role.Validate name", func(s *specs.Spec) {
		s.It("names a role whose name is badly formed before its kind", func(ctx *specs.Context) {
			role := domain.Role{Name: "Reviewer-Code", Kind: "deploy", Mode: domain.ModeBlocking}

			err := role.Validate()

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(`invalid value: role name "Reviewer-Code", want only lowercase letters, digits and hyphens`)
		})
	})

	specs.Describe(t, "Role.AlwaysRerun", func(s *specs.Spec) {
		s.It("is off unless the role is marked", func(ctx *specs.Context) {
			var r domain.Role
			ctx.Expect(json.Unmarshal([]byte(`{"name":"reviewer-code","kind":"review","mode":"blocking"}`), &r)).To(specs.BeNil())

			ctx.Expect(r.AlwaysRerun).ToEqual(false)
		})

		s.It("reads always_rerun next to the mode", func(ctx *specs.Context) {
			var r domain.Role
			ctx.Expect(json.Unmarshal([]byte(`{"name":"reviewer-security","kind":"review","mode":"blocking","always_rerun":true}`), &r)).To(specs.BeNil())

			ctx.Expect(r.AlwaysRerun).ToEqual(true)
		})
	})
}
