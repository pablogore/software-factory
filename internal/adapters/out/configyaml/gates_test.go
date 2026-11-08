package configyaml_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
)

func TestLoadGates(t *testing.T) {
	specs.Describe(t, "LoadGates", func(s *specs.Spec) {
		s.It("loads the test count command split on spaces", func(ctx *specs.Context) {
			g, err := configyaml.LoadGates([]byte("count_command: go test -json -count=1 ./...\n"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(g.CountCommand).ToEqual([]string{"go", "test", "-json", "-count=1", "./..."})
		})

		s.It("loads no count command when the file sets none", func(ctx *specs.Context) {
			g, err := configyaml.LoadGates([]byte("count_command: \"\"\n"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(g.CountCommand).To(specs.BeEmpty())
		})

		s.It("refuses an unknown key, naming it", func(ctx *specs.Context) {
			_, err := configyaml.LoadGates([]byte("count_comand: go test ./...\n"))

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("count_comand"))
		})

		s.It("refuses an empty file", func(ctx *specs.Context) {
			_, err := configyaml.LoadGates(nil)

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})
	})
}
