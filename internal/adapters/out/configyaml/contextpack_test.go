package configyaml_test

import (
	"os"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
)

// docExample is the YAML block that follows the line naming file in doc.
func docExample(ctx *specs.Context, doc, file string) string {
	data, err := os.ReadFile(doc)
	ctx.Expect(err).To(specs.BeNil())
	_, after, ok := strings.Cut(string(data), "`"+file+"`\n```yaml\n")
	ctx.Expect(ok).To(specs.BeTrue())
	block, _, ok := strings.Cut(after, "```")
	ctx.Expect(ok).To(specs.BeTrue())
	return block
}

func TestContextPackRoleExample(t *testing.T) {
	specs.Describe(t, "the security-deep example of 04-roles.md", func(s *specs.Spec) {
		s.It("loads, with context_pack and submit_result as its tools", func(ctx *specs.Context) {
			r, err := configyaml.LoadRole([]byte(docExample(ctx, "../../../../docs/factoryd/04-roles.md", "roles/security-deep.yaml")))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(r.Name).ToEqual("security-deep")
			ctx.Expect(r.Kind).ToEqual(domain.KindReview)
			ctx.Expect(r.Tools).ToEqual([]string{"context_pack", "submit_result"})
		})
	})
}
