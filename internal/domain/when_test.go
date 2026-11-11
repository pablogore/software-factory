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

type ratingRow struct {
	name   string
	when   domain.When
	rating domain.Rating
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

func risk(levels ...domain.Risk) domain.Condition { return domain.Condition{Risk: levels} }

func surfaces(names ...string) domain.Condition { return domain.Condition{Surfaces: names} }

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
			ok, reason := r.when.Applies(r.diff, domain.Rating{Risk: domain.RiskMedium})

			ctx.Expect(ok).ToEqual(r.want)
			ctx.Expect(reason).ToEqual(r.reason)
		})
	})

	specs.Describe(t, "When.Applies on intake's rating", func(s *specs.Spec) {
		diff := &domain.DiffSummary{Paths: []string{"internal/a.go"}, Lines: 120}
		low := domain.Rating{Risk: domain.RiskLow, Surfaces: []string{"api"}}
		high := domain.Rating{Risk: domain.RiskHigh, Surfaces: []string{"api", "ui"}}
		none := domain.Rating{Risk: domain.RiskMedium, Surfaces: []string{}}
		rows := []ratingRow{
			{"applies when the intake risk is listed", domain.When{Any: []domain.Condition{risk(domain.RiskHigh)}}, high, true, ""},
			{"does not apply when the intake risk is not listed, and says which", domain.When{Any: []domain.Condition{risk(domain.RiskMedium, domain.RiskHigh)}}, low, false,
				"when does not apply: the intake risk is low, not medium, high"},
			{"counts a job intake never rated as high risk", domain.When{Any: []domain.Condition{risk(domain.RiskHigh)}}, domain.Rating{}, true, ""},
			{"does not apply to a job intake never rated when high is not listed", domain.When{Any: []domain.Condition{risk(domain.RiskLow)}}, domain.Rating{}, false,
				"when does not apply: the intake risk is high, not low"},
			{"applies when the intake shares a surface", domain.When{Any: []domain.Condition{surfaces("security", "ui")}}, high, true, ""},
			{"does not apply when the intake shares no surface, and says which", domain.When{Any: []domain.Condition{surfaces("security", "ui")}}, low, false,
				"when does not apply: no intake surface matches surfaces security, ui"},
			{"does not apply when the intake left no surface", domain.When{Any: []domain.Condition{surfaces("ui")}}, none, false,
				"when does not apply: no intake surface matches surfaces ui"},
			{"applies on surfaces of a job intake never rated, which it can't evaluate", domain.When{Any: []domain.Condition{surfaces("ui")}}, domain.Rating{}, true, ""},
			{"applies when either risk or surfaces holds under any", domain.When{Any: []domain.Condition{risk(domain.RiskHigh), surfaces("api")}}, low, true, ""},
			{"needs both risk and paths of one condition to hold", domain.When{Any: []domain.Condition{{Risk: []domain.Risk{domain.RiskLow}, Paths: []string{"ui/**"}}}}, low, false,
				"when does not apply: no changed file matches paths ui/**"},
			{"checks risk even beside a key the job can't evaluate yet", domain.When{Any: []domain.Condition{{Risk: []domain.Risk{domain.RiskHigh}, Labels: []string{"security"}}}}, low, false,
				"when does not apply: the intake risk is low, not high"},
		}
		specs.Table(s, rows, func(r ratingRow) string { return r.name }, func(ctx *specs.Context, r ratingRow) {
			ok, reason := r.when.Applies(diff, r.rating)

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
			{"rejects an unknown risk level", risk("critical"), `role reviewer-big: invalid value: when risk "critical", want low, medium or high`},
			{"rejects a blank surface", surfaces(" "), `role reviewer-big: invalid value: when surface " " is blank`},
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
