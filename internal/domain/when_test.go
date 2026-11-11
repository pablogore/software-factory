package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

type globRow struct {
	name    string
	pattern string
	path    string
	want    bool
}

type appliesRow struct {
	name   string
	when   domain.When
	diff   *domain.DiffSummary
	want   bool
	reason string
}

type whenErrRow struct {
	name string
	cond domain.Condition
	want string
}

func paths(p ...string) domain.Condition { return domain.Condition{Paths: p} }

func lines(expr string) domain.Condition { return domain.Condition{DiffLines: expr} }

func TestWhen(t *testing.T) {
	specs.Describe(t, "MatchGlob", func(s *specs.Spec) {
		rows := []globRow{
			{"matches a file under a ** folder", "src/auth/**", "src/auth/a/b.go", true},
			{"matches a file right under a ** folder", "src/auth/**", "src/auth/b.go", true},
			{"matches a leading ** at any depth", "**/middleware/**", "a/b/middleware/c.go", true},
			{"matches a leading ** at the root", "**/middleware/**", "middleware/c.go", true},
			{"matches a segment pattern", "docs/*.md", "docs/a.md", true},
			{"does not match a * across folders", "docs/*.md", "docs/x/a.md", false},
			{"does not match another folder", "src/auth/**", "src/api/a.go", false},
			{"does not match a folder's name as a prefix", "src/auth/**", "src/authz/a.go", false},
		}
		specs.Table(s, rows, func(r globRow) string { return r.name }, func(ctx *specs.Context, r globRow) {
			ctx.Expect(domain.MatchGlob(r.pattern, r.path)).ToEqual(r.want)
		})
	})

	specs.Describe(t, "When.Applies", func(s *specs.Spec) {
		diff := &domain.DiffSummary{Paths: []string{"internal/a.go", "README.md"}, Lines: 120}
		rows := []appliesRow{
			{"applies with no conditions", domain.When{}, diff, true, ""},
			{"applies when a changed file matches paths", domain.When{Any: []domain.Condition{paths("ui/**", "internal/**")}}, diff, true, ""},
			{"applies when the diff size meets diff_lines", domain.When{Any: []domain.Condition{lines(">= 100")}}, diff, true, ""},
			{"applies when any one condition holds", domain.When{Any: []domain.Condition{paths("ui/**"), lines("< 200")}}, diff, true, ""},
			{"applies on labels, which the job can't evaluate yet", domain.When{Any: []domain.Condition{{Labels: []string{"security"}}}}, diff, true, ""},
			{"applies on deps_changed, which the job can't evaluate yet", domain.When{Any: []domain.Condition{{DepsChanged: true}}}, diff, true, ""},
			{"applies with no diff to read", domain.When{Any: []domain.Condition{paths("ui/**")}}, nil, true, ""},
			{"does not apply when no changed file matches, and says which paths", domain.When{Any: []domain.Condition{paths("ui/**")}}, diff, false,
				"when does not apply: no changed file matches paths ui/**"},
			{"does not apply when no condition holds, and gives each reason", domain.When{Any: []domain.Condition{paths("ui/**", "web/**"), lines("> 120")}}, diff, false,
				"when does not apply: no changed file matches paths ui/**, web/**; the diff has 120 lines, not > 120"},
			{"needs every key of one condition to hold", domain.When{Any: []domain.Condition{{Paths: []string{"internal/**"}, DiffLines: ">= 500"}}}, diff, false,
				"when does not apply: the diff has 120 lines, not >= 500"},
		}
		specs.Table(s, rows, func(r appliesRow) string { return r.name }, func(ctx *specs.Context, r appliesRow) {
			ok, reason := r.when.Applies(r.diff)

			ctx.Expect(ok).ToEqual(r.want)
			ctx.Expect(reason).ToEqual(r.reason)
		})
	})

	specs.Describe(t, "Role.Validate on when", func(s *specs.Spec) {
		rows := []whenErrRow{
			{"rejects diff_lines with no operator", lines("200"), `role reviewer-big: invalid value: diff_lines "200", want an operator (>=, <=, ==, >, <) and a number`},
			{"rejects diff_lines with no number", lines(">= many"), `role reviewer-big: invalid value: diff_lines ">= many", want an operator (>=, <=, ==, >, <) and a number`},
			{"rejects a malformed path pattern", paths("src/[a"), `role reviewer-big: invalid value: when path "src/[a"`},
			{"rejects an empty path pattern", paths(""), `role reviewer-big: invalid value: when path ""`},
		}
		specs.Table(s, rows, func(r whenErrRow) string { return r.name }, func(ctx *specs.Context, r whenErrRow) {
			role := domain.Role{Name: "reviewer-big", Kind: domain.KindReview, Mode: domain.ModeBlocking, When: domain.When{Any: []domain.Condition{r.cond}}}

			err := role.Validate()

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).ToEqual(r.want)
		})

		s.It("accepts every operator diff_lines takes", func(ctx *specs.Context) {
			for _, expr := range []string{">= 1", "<= 1", "== 1", "> 1", "< 1", ">=200"} {
				role := domain.Role{Name: "reviewer-big", Kind: domain.KindReview, Mode: domain.ModeBlocking, When: domain.When{Any: []domain.Condition{lines(expr)}}}
				ctx.Expect(role.Validate()).To(specs.BeNil())
			}
		})
	})
}
