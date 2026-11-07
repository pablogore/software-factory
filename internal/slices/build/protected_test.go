package build_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/slices/build"
)

type protectedRow struct {
	path      string
	protected bool
}

func TestProtectedTouched(t *testing.T) {
	specs.Describe(t, "build.ProtectedTouched", func(s *specs.Spec) {
		rows := []protectedRow{
			{"Makefile", true},
			{"internal/arch_test.go", true},
			{".github/workflows/ci.yml", true},
			{".github/workflows/sub/x.yml", true},
			{"AGENTS.md", true},
			{".golangci.yml", true},
			{"docs/factoryd/00-vision.md", true},
			{"docs/factoryd/07-arquitectura.md", true},
			{"docs/factoryd/README.md", false},
			{"docs/factoryd/backlog/01-x.md", false},
			{"cmd/Makefile", false},
			{"internal/slices/build/arch_test.go", false},
			{".github/CODEOWNERS", false},
			{"README.md", false},
		}
		specs.Table(s, rows, func(r protectedRow) string { return r.path }, func(ctx *specs.Context, r protectedRow) {
			modified := build.ProtectedTouched([]domain.FileChange{{Path: r.path, Kind: domain.ChangeModified}})
			deleted := build.ProtectedTouched([]domain.FileChange{{Path: r.path, Kind: domain.ChangeDeleted}})
			added := build.ProtectedTouched([]domain.FileChange{{Path: r.path, Kind: domain.ChangeAdded}})

			ctx.Expect(added).To(specs.BeEmpty())
			if r.protected {
				ctx.Expect(modified).ToEqual([]string{"the diff modifies the protected path " + r.path})
				ctx.Expect(deleted).ToEqual([]string{"the diff deletes the protected path " + r.path})
				return
			}
			ctx.Expect(modified).To(specs.BeEmpty())
			ctx.Expect(deleted).To(specs.BeEmpty())
		})
	})
}
