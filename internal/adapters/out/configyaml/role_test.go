package configyaml_test

import (
	"os"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

type rerunRow struct {
	name string
	yaml string
	want bool
}

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

		s.When("the role file sets always_rerun", func(s *specs.Spec) {
			rows := []rerunRow{
				{"loads always_rerun true as always-rerun on", "always_rerun: true\n", true},
				{"loads always_rerun false as always-rerun off", "always_rerun: false\n", false},
				{"loads a role without always_rerun as always-rerun off", "", false},
			}
			specs.Table(s, rows, func(r rerunRow) string {
				return r.name
			}, func(ctx *specs.Context, r rerunRow) {
				role, err := configyaml.LoadRole([]byte("name: reviewer-security\nkind: review\nprompt: roles/reviewer-security.md\nmode: blocking\n" + r.yaml))

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(role.AlwaysRerun).ToEqual(r.want)
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
				{
					"names the role whose always_rerun is a word",
					"name: security-deep\nkind: review\nmode: shadow\nalways_rerun: yes\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: line 4: always_rerun "yes", want true or false`,
				},
				{
					"names the role whose always_rerun is a quoted true",
					"name: security-deep\nkind: review\nmode: shadow\nalways_rerun: \"true\"\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: line 4: always_rerun "true", want true or false`,
				},
				{
					"names the role whose always_rerun is empty",
					"name: security-deep\nkind: review\nmode: shadow\nalways_rerun:\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: line 4: always_rerun "", want true or false`,
				},
				{
					"names the role whose always_rerun is a list",
					"name: security-deep\nkind: review\nmode: shadow\nalways_rerun: [true]\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: line 4: always_rerun is a list, want true or false`,
				},
				{
					"names the role whose always_rerun is a map",
					"name: security-deep\nkind: review\nmode: shadow\nalways_rerun: {on: true}\n",
					domain.ErrInvalidValue,
					`role security-deep: invalid value: line 4: always_rerun is a map, want true or false`,
				},
			}
			specs.Table(s, rows, func(r badRoleRow) string {
				return r.name
			}, func(ctx *specs.Context, r badRoleRow) {
				_, err := configyaml.LoadRole([]byte(r.yaml))

				// Printed so `go test -v` shows the operator's message.
				ctx.Testing().Log(err)
				ctx.Expect(err).To(specs.MatchError(r.sentinel))
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})

		s.It("loads a when condition that merges keys from an anchor", func(ctx *specs.Context) {
			r, err := configyaml.LoadRole([]byte("name: security-deep\nkind: review\nmode: shadow\nwhen:\n  any:\n    - &c {paths: [x]}\n    - <<: *c\n      labels: [y]\n"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(r.When.Any).ToEqual([]domain.Condition{
				{Paths: []string{"x"}},
				{Paths: []string{"x"}, Labels: []string{"y"}},
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
					`parse role: invalid value: line 6: unknown key "path"`,
				},
				{
					"names the unknown key in when",
					"name: security-deep\nkind: review\nmode: shadow\nwhen:\n  all:\n    - paths: [src/**]\n",
					`parse role: invalid value: line 5: unknown key "all"`,
				},
				{
					"names the unknown key in budget",
					"name: security-deep\nkind: review\nmode: shadow\nbudget:\n  attempts: 2\n  dollars: 5\n",
					`parse role: invalid value: line 6: unknown key "dollars"`,
				},
				{
					"names the unknown key in a map budget aliases",
					"name: security-deep\nkind: review\nmode: shadow\nwhen: &w {any: [{paths: [x]}]}\nbudget: *w\n",
					`parse role: invalid value: line 4: unknown key "any"`,
				},
				{
					"names the unknown key in a map merged into a when condition",
					"name: security-deep\nkind: review\nmode: shadow\ndescription: &c {path: [x]}\nwhen:\n  any:\n    - <<: *c\n",
					`parse role: invalid value: line 4: unknown key "path"`,
				},
				{
					"rejects a role file holding only a document marker",
					"---\n",
					"parse role: invalid value: file is empty",
				},
				{
					"rejects an empty role file",
					"",
					"parse role: invalid value: file is empty",
				},
				{
					"rejects a role file with only comments",
					"# reviewer-security\n",
					"parse role: invalid value: file is empty",
				},
			}
			specs.Table(s, rows, func(r brokenRow) string {
				return r.name
			}, func(ctx *specs.Context, r brokenRow) {
				_, err := configyaml.LoadRole([]byte(r.yaml))

				// Printed so `go test -v` shows the operator's message.
				ctx.Testing().Log(err)
				ctx.Expect(err == nil).ToEqual(false)
				ctx.Expect(err.Error()).ToEqual(r.want)
			})
		})
	})
}
