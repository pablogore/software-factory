package childenv_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
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

func TestHomes(t *testing.T) {
	specs.Describe(t, "childenv.Homes", func(s *specs.Spec) {
		s.It("makes one HOME per job under its root, the same on every call", func(ctx *specs.Context) {
			root := ctx.Testing().TempDir()
			homes := childenv.Homes{Root: root}

			first, err := homes.For("j-1")
			ctx.Expect(err).To(specs.BeNil())
			again, err := homes.For("j-1")
			ctx.Expect(err).To(specs.BeNil())
			other, err := homes.For("j-2")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(first).ToEqual(filepath.Join(root, "j-1"))
			ctx.Expect(again).ToEqual(first)
			ctx.Expect(other).ToEqual(filepath.Join(root, "j-2"))
			info, err := os.Stat(first)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(info.IsDir()).To(specs.BeTrue())
		})

		s.It("keeps them in factoryd's data directory by default", func(ctx *specs.Context) {
			cache, err := os.UserCacheDir()
			ctx.Expect(err).To(specs.BeNil())

			homes, err := childenv.DefaultHomes()

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(homes.Root).ToEqual(filepath.Join(cache, "factoryd", "homes"))
		})

		s.It("refuses a job id that would land outside its root", func(ctx *specs.Context) {
			homes := childenv.Homes{Root: ctx.Testing().TempDir()}
			for _, id := range []domain.JobID{"..", ".", "../x", `a\b`} {
				_, err := homes.For(id)

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			}
		})

		s.It("is an error without a root or a job id", func(ctx *specs.Context) {
			_, err := childenv.Homes{}.For("j-1")
			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))

			_, err = childenv.Homes{Root: ctx.Testing().TempDir()}.For("")
			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
