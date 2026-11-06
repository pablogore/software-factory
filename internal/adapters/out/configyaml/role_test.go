package configyaml_test

import (
	"os"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

type badRoleRow struct {
	name     string
	yaml     string
	sentinel error
	want     string
}

func TestLoadRole(t *testing.T) {
	specs.Describe(t, "LoadRole", func(s *specs.Spec) {
		s.It("loads security-deep with its when and mode", func(ctx *specs.Context) {
			data, err := os.ReadFile("testdata/roles/security-deep.yaml")
			ctx.Expect(err).To(specs.BeNil())

			r, err := configyaml.LoadRole(data)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(r).ToEqual(domain.Role{
				Name:        "security-deep",
				Kind:        domain.KindReview,
				Description: "Deep review of auth, sessions, permissions and dependencies",
				Prompt:      "roles/security-deep.md",
				Agent:       "codex",
				Model:       "gpt-5.5",
				When: domain.When{Any: []domain.Condition{
					{Paths: []string{"src/auth/**", "src/api/**", "**/middleware/**"}},
					{Labels: []string{"security", "auth", "payments"}},
					{DepsChanged: true},
					{DiffLines: ">= 200"},
				}},
				Mode: domain.ModeShadow,
			})
		})

		s.When("the role file is wrong", func(s *specs.Spec) {
			rows := []badRoleRow{
				{
					"names the role and the unknown mode",
					"name: security-deep\nkind: review\nprompt: roles/security-deep.md\nmode: loud\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: mode "loud"`,
				},
				{
					"rejects a role with no mode",
					"name: security-deep\nkind: review\nprompt: roles/security-deep.md\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: mode ""`,
				},
				{
					"names the role and the unknown kind",
					"name: security-deep\nkind: deploy\nprompt: roles/security-deep.md\nmode: shadow\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: kind "deploy"`,
				},
			}
			specs.Table(s, rows, func(r badRoleRow) string {
				return r.name
			}, func(ctx *specs.Context, r badRoleRow) {
				_, err := configyaml.LoadRole([]byte(r.yaml))

				ctx.Expect(err).To(specs.MatchError(r.sentinel))
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})

		s.When("a key is misspelled", func(s *specs.Spec) {
			rows := []brokenRow{
				{
					"names the unknown top-level key",
					"name: security-deep\nkind: review\nprompt: roles/security-deep.md\nmode: shadow\nmodel_: gpt-5.5\n",
					`parse role: invalid value: line 5: unknown key "model_"`,
				},
				{
					"names the unknown key in a when condition",
					"name: security-deep\nkind: review\nmode: shadow\nwhen:\n  any:\n    - path: [src/**]\n",
					"parse role: yaml: unmarshal errors:\n  line 6: field path not found in type configyaml.conditionFile",
				},
			}
			specs.Table(s, rows, func(r brokenRow) string {
				return r.name
			}, func(ctx *specs.Context, r brokenRow) {
				_, err := configyaml.LoadRole([]byte(r.yaml))

				ctx.Expect(err == nil).ToEqual(false)
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})
	})
}
