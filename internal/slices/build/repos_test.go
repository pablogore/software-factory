package build_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/slices/build"
)

func TestReposChanged(t *testing.T) {
	specs.Describe(t, "build.ReposChanged", func(s *specs.Spec) {
		nested := domain.FileChange{Path: "vendorx/", Kind: domain.ChangeAdded, Repo: domain.RepoNested}

		s.It("names a nested repo, whatever the roles allow", func(ctx *specs.Context) {
			want := []string{"the worktree has vendorx/, a folder with its own .git that hides its files from the gate"}

			ctx.Expect(build.ReposChanged([]domain.FileChange{nested}, nil)).ToEqual(want)
			ctx.Expect(build.ReposChanged([]domain.FileChange{nested}, []domain.Role{builder("vendorx/")})).ToEqual(want)
		})

		s.It("names a submodule added, modified or deleted when no build role has a role file or allowed paths", func(ctx *specs.Context) {
			for kind, verb := range map[domain.ChangeKind]string{domain.ChangeAdded: "adds", domain.ChangeModified: "modifies", domain.ChangeDeleted: "deletes"} {
				changes := []domain.FileChange{{Path: "lib/sub", Kind: kind, Repo: domain.RepoSubmodule}}
				want := []string{"the diff " + verb + " the submodule lib/sub, which no build role's allowed paths cover"}

				ctx.Expect(build.ReposChanged(changes, nil)).ToEqual(want)
				ctx.Expect(build.ReposChanged(changes, []domain.Role{builder()})).ToEqual(want)
				ctx.Expect(build.ReposChanged(changes, []domain.Role{builder("internal/")})).ToEqual(want)
			}
		})

		s.It("allows a submodule any build role's allowed paths cover", func(ctx *specs.Context) {
			changes := []domain.FileChange{{Path: "lib/sub", Kind: domain.ChangeModified, Repo: domain.RepoSubmodule}}
			other := domain.Role{Name: "deps", Kind: domain.KindBuild, Mode: domain.ModeBlocking, AllowedPaths: []string{"lib/"}}

			ctx.Expect(build.ReposChanged(changes, []domain.Role{builder("lib/sub")})).To(specs.BeEmpty())
			ctx.Expect(build.ReposChanged(changes, []domain.Role{builder("internal/"), other})).To(specs.BeEmpty())
		})

		s.It("leaves plain files to the other rules", func(ctx *specs.Context) {
			changes := []domain.FileChange{{Path: "vendorx/a_test.go", Kind: domain.ChangeAdded}, {Path: "Makefile", Kind: domain.ChangeModified}}

			ctx.Expect(build.ReposChanged(changes, nil)).To(specs.BeEmpty())
		})
	})
}
