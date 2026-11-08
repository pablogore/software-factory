package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
)

// configValidateRoot is a configuration root with a builder on opus and a
// reviewer on reviewer's agent and model.
func configValidateRoot(ctx *specs.Context, reviewer string) string {
	dir := roleDemoConfig(ctx, "agent: claude\nmodel: opus\n")
	ctx.Expect(os.WriteFile(filepath.Join(dir, "roles", "reviewer.md"), []byte("Review the branch.\n"), 0o600)).To(specs.BeNil())
	data := "name: reviewer\nkind: review\nprompt: roles/reviewer.md\nmode: blocking\n" + reviewer
	ctx.Expect(os.WriteFile(filepath.Join(dir, "roles", "reviewer.yaml"), []byte(data), 0o600)).To(specs.BeNil())
	return dir
}

func TestConfigValidate(t *testing.T) {
	specs.Describe(t, "factoryd config-validate", func(s *specs.Spec) {
		s.It("warns that the repo's security reviewer uses the builder's agent and model, and succeeds", func(ctx *specs.Context) {
			var out bytes.Buffer

			err := configValidate([]string{"--config", "../.."}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).ToEqual("warning: reviewer-security uses the same agent and model as builder: claude opus\nconfiguration in ../.. is valid, with warnings\n")
		})

		s.It("says the configuration is valid, with no warnings, when no reviewer matches the builder", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: claude\nmodel: sonnet\n")
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).ToEqual("configuration in " + dir + " is valid\n")
		})

		s.It("does not warn about a reviewer on the builder's model but another agent", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: gemini\nmodel: opus\n")
			var out bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(out.String()).To(specs.Not(specs.Contain("warning")))
		})

		s.It("fails with role-demo's error when a role names an agent that is not configured", func(ctx *specs.Context) {
			dir := configValidateRoot(ctx, "agent: nadie\nmodel: opus\n")
			var out, demo bytes.Buffer

			err := configValidate([]string{"--config", dir}, &out)
			demoErr := roleDemo(context.Background(), []string{"--config", dir, "--role", "reviewer"}, &demo)

			ctx.Expect(err).To(specs.MatchError(configyaml.ErrUnknownAgent))
			ctx.Expect(err.Error()).To(specs.Contain(`"nadie"`))
			ctx.Expect(err.Error()).ToEqual(demoErr.Error())
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})
	})
}
