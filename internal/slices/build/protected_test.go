package build_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/slices/build"
)

// protectedRow is a path, whether modifying or deleting it fails the gate,
// and whether adding it does too.
type protectedRow struct {
	path      string
	protected bool
	noAdd     bool
}

func TestProtectedTouched(t *testing.T) {
	specs.Describe(t, "build.ProtectedTouched", func(s *specs.Spec) {
		rows := []protectedRow{
			{"Makefile", true, false},
			{"GNUmakefile", true, true},
			{"makefile", true, true},
			{"internal/arch_test.go", true, false},
			{".github/workflows/ci.yml", true, true},
			{".github/workflows/sub/x.yml", true, true},
			{".github/CODEOWNERS", true, true},
			{".github/actions/setup/action.yml", true, true},
			{"AGENTS.md", true, false},
			{".golangci.yml", true, false},
			{"docs/factoryd/00-vision.md", true, false},
			{"docs/factoryd/07-arquitectura.md", true, false},
			{".factory/gates.yaml", true, false},
			{".factory/other.yaml", true, false},
			{"docs/.factory/gates.yaml", false, false},
			{"docs/factoryd/README.md", false, false},
			{"docs/factoryd/backlog/01-x.md", false, false},
			{"cmd/Makefile", false, false},
			{"cmd/GNUmakefile", false, false},
			{"internal/slices/build/arch_test.go", false, false},
			{"docs/.github/x.yml", false, false},
			{"README.md", false, false},
		}
		specs.Table(s, rows, func(r protectedRow) string { return r.path }, func(ctx *specs.Context, r protectedRow) {
			modified := build.ProtectedTouched([]domain.FileChange{{Path: r.path, Kind: domain.ChangeModified}})
			deleted := build.ProtectedTouched([]domain.FileChange{{Path: r.path, Kind: domain.ChangeDeleted}})
			added := build.ProtectedTouched([]domain.FileChange{{Path: r.path, Kind: domain.ChangeAdded}})

			if r.noAdd {
				ctx.Expect(added).ToEqual([]string{"the diff adds the protected path " + r.path})
			} else {
				ctx.Expect(added).To(specs.BeEmpty())
			}
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
