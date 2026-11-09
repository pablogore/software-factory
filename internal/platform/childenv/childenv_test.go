package childenv_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// names are the variable names of env.
func names(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		out = append(out, name)
	}
	return out
}

func TestBase(t *testing.T) {
	specs.Describe(t, "childenv.Base", func(s *specs.Spec) {
		s.It("leaves out factoryd's tokens and every variable it does not allow", func(ctx *specs.Context) {
			ctx.Testing().Setenv("GITHUB_TOKEN", "ghp_secret")
			ctx.Testing().Setenv("GH_TOKEN", "gho_secret")
			ctx.Testing().Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
			ctx.Testing().Setenv("FACTORY_TEST_OTHER", "other")

			got := childenv.Base("/work/job-1")

			ctx.Expect(strings.Join(got, "\n")).To(specs.Not(specs.Contain("secret")))
			for _, name := range names(got) {
				if !strings.HasPrefix(name, "LC_") {
					ctx.Expect([]string{"PATH", "LANG", "TMPDIR", "HOME", "GOPATH", "GOMODCACHE", "GOCACHE", "GOLANGCI_LINT_CACHE"}).To(specs.Contain(name))
				}
			}
		})

		s.It("passes PATH, the locale and TMPDIR as they are and sets HOME to the worktree", func(ctx *specs.Context) {
			ctx.Testing().Setenv("PATH", "/usr/bin:/bin")
			ctx.Testing().Setenv("LANG", "es_AR.UTF-8")
			ctx.Testing().Setenv("LC_ALL", "es_AR.UTF-8")
			ctx.Testing().Setenv("LC_CTYPE", "UTF-8")
			ctx.Testing().Setenv("TMPDIR", "/tmp/factoryd")
			ctx.Testing().Setenv("HOME", "/Users/operator")

			got := childenv.Base("/work/job-1")

			ctx.Expect(got).To(specs.ContainAllOf("PATH=/usr/bin:/bin", "LANG=es_AR.UTF-8", "LC_ALL=es_AR.UTF-8", "LC_CTYPE=UTF-8", "TMPDIR=/tmp/factoryd", "HOME=/work/job-1"))
		})

		s.It("keeps the Go caches where factoryd's are, not under the new HOME", func(ctx *specs.Context) {
			ctx.Testing().Setenv("HOME", "/Users/operator")
			ctx.Testing().Setenv("GOCACHE", "/cache/go-build")
			ctx.Testing().Setenv("GOMODCACHE", "/cache/mod")
			for _, k := range []string{"GOPATH", "GOLANGCI_LINT_CACHE"} {
				ctx.Testing().Setenv(k, "")
				ctx.Expect(os.Unsetenv(k)).To(specs.BeNil())
			}
			cache, err := os.UserCacheDir()
			ctx.Expect(err).To(specs.BeNil())

			got := childenv.Base("/work/job-1")

			ctx.Expect(got).To(specs.ContainAllOf(
				"GOCACHE=/cache/go-build",
				"GOMODCACHE=/cache/mod",
				"GOPATH="+filepath.Join("/Users/operator", "go"),
				"GOLANGCI_LINT_CACHE="+filepath.Join(cache, "golangci-lint"),
			))
		})
	})
}
