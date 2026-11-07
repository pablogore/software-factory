package build_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/slices/build"
)

type allowedRow struct {
	path    string
	allowed bool
}

func builder(allowed ...string) domain.Role {
	return domain.Role{Name: "builder", Kind: domain.KindBuild, Mode: domain.ModeBlocking, AllowedPaths: allowed}
}

func TestOutsideAllowed(t *testing.T) {
	specs.Describe(t, "build.OutsideAllowed", func(s *specs.Spec) {
		roles := []domain.Role{builder("internal/slices/build/", "docs/*.md")}
		rows := []allowedRow{
			{"internal/slices/build/gate.go", true},
			{"internal/slices/build/sub/x.go", true},
			{"docs/notes.md", true},
			{"docs/sub/notes.md", false},
			{"internal/slices/builder/x.go", false},
			{"internal/slices/build", false},
			{"README.md", false},
		}
		specs.Table(s, rows, func(r allowedRow) string { return r.path }, func(ctx *specs.Context, r allowedRow) {
			for kind, verb := range map[domain.ChangeKind]string{domain.ChangeAdded: "adds", domain.ChangeModified: "modifies", domain.ChangeDeleted: "deletes"} {
				got := build.OutsideAllowed([]domain.FileChange{{Path: r.path, Kind: kind}}, roles)

				if r.allowed {
					ctx.Expect(got).To(specs.BeEmpty())
					continue
				}
				ctx.Expect(got).ToEqual([]string{"the diff " + verb + " " + r.path + ", outside the allowed paths of build role builder"})
			}
		})

		changes := []domain.FileChange{{Path: "README.md", Kind: domain.ChangeModified}}

		s.It("limits nothing when no build role has a role file", func(ctx *specs.Context) {
			ctx.Expect(build.OutsideAllowed(changes, nil)).To(specs.BeEmpty())
		})

		s.It("limits nothing when the build role declares no allowed paths", func(ctx *specs.Context) {
			ctx.Expect(build.OutsideAllowed(changes, []domain.Role{builder()})).To(specs.BeEmpty())
		})

		s.It("allows a file any of several build roles allows", func(ctx *specs.Context) {
			other := domain.Role{Name: "docs-builder", Kind: domain.KindBuild, Mode: domain.ModeBlocking, AllowedPaths: []string{"README.md"}}
			two := []domain.Role{builder("internal/"), other}

			ctx.Expect(build.OutsideAllowed(changes, two)).To(specs.BeEmpty())
			ctx.Expect(build.OutsideAllowed([]domain.FileChange{{Path: "go.mod", Kind: domain.ChangeModified}}, two)).
				ToEqual([]string{"the diff modifies go.mod, outside the allowed paths of build role builder, docs-builder"})
		})
	})

	specs.Describe(t, "build.Roles", func(s *specs.Spec) {
		s.It("is the loaded roles of the build stage only", func(ctx *specs.Context) {
			b := builder("internal/")
			p := domain.Pipeline{
				Stages: []domain.Stage{
					{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
					{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
				},
				Roles: []domain.Role{{Name: "reviewer-code", Kind: domain.KindReview, Mode: domain.ModeBlocking}, b},
			}

			ctx.Expect(build.Roles(p)).ToEqual([]domain.Role{b})
		})
	})
}
