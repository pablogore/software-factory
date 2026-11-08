package configyaml_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
)

func TestLoadDevServer(t *testing.T) {
	specs.Describe(t, "LoadDevServer", func(s *specs.Spec) {
		s.It("loads the dev server command split on spaces", func(ctx *specs.Context) {
			d, err := configyaml.LoadDevServer([]byte("command: npm run dev\n"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(d.Command).ToEqual([]string{"npm", "run", "dev"})
		})

		s.It("loads the variables the command runs with", func(ctx *specs.Context) {
			d, err := configyaml.LoadDevServer([]byte("command: npm run dev\nenv:\n  NODE_ENV: development\n"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(d.Env).ToEqual(map[string]string{"NODE_ENV": "development"})
		})

		s.It("refuses PORT among the variables, as the job's port is set by factoryd", func(ctx *specs.Context) {
			_, err := configyaml.LoadDevServer([]byte("command: npm run dev\nenv:\n  PORT: \"80\"\n"))

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain(`env "PORT"`))
		})

		s.It("refuses an unknown key, naming it", func(ctx *specs.Context) {
			_, err := configyaml.LoadDevServer([]byte("comand: npm run dev\n"))

			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.Contain("comand"))
		})
	})
}
