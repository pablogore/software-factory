package domain_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func validFinding() domain.Finding {
	return domain.Finding{File: "internal/x.go", Line: 12, Severity: "major", Category: "bug", Role: "reviewer-code"}
}

type missingFieldRow struct {
	field string
	edit  func(*domain.Finding)
}

type verdictRow struct {
	name string
	res  domain.Result
	ok   bool
}

func TestResult(t *testing.T) {
	specs.Describe(t, "Result", func(s *specs.Spec) {
		s.When("a review finding lacks a required field", func(s *specs.Spec) {
			rows := []missingFieldRow{
				{"file", func(f *domain.Finding) { f.File = "" }},
				{"severity", func(f *domain.Finding) { f.Severity = "" }},
			}
			specs.Table(s, rows, func(r missingFieldRow) string {
				return "names the missing " + r.field
			}, func(ctx *specs.Context, r missingFieldRow) {
				f := validFinding()
				r.edit(&f)
				res := domain.ReviewResult{Verdict: domain.ReviewChanges, Findings: []domain.Finding{validFinding(), f}}

				err := res.Validate()

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(err.Error()).ToEqual("findings[1]: missing field: " + r.field)
			})
		})

		s.It("accepts a review whose findings are complete", func(ctx *specs.Context) {
			res := domain.ReviewResult{Verdict: domain.ReviewChanges, Findings: []domain.Finding{validFinding()}}
			ctx.Expect(res.Validate()).To(specs.BeNil())
		})

		s.When("checking verdicts", func(s *specs.Spec) {
			rows := []verdictRow{
				{"intake ACCEPT", domain.IntakeResult{Decision: domain.IntakeAccept}, true},
				{"intake MAYBE", domain.IntakeResult{Decision: "MAYBE"}, false},
				{"spec with body", domain.SpecResult{Spec: "# S1"}, true},
				{"spec empty", domain.SpecResult{}, false},
				{"build BLOCKED", domain.BuildResult{Status: domain.BuildBlocked}, true},
				{"build missing status", domain.BuildResult{}, false},
				{"review PASS", domain.ReviewResult{Verdict: domain.ReviewPass}, true},
				{"review LGTM", domain.ReviewResult{Verdict: "LGTM"}, false},
				{"approve ESCALATE", domain.ApproveResult{Decision: domain.ApproveEscalate}, true},
				{"approve MERGE", domain.ApproveResult{Decision: "MERGE"}, false},
				{"task with files", domain.TaskResult{Produces: []string{"CHANGELOG.md"}}, true},
			}
			specs.Table(s, rows, func(r verdictRow) string {
				return r.name
			}, func(ctx *specs.Context, r verdictRow) {
				err := r.res.Validate()
				ctx.Expect(err == nil).ToEqual(r.ok)
			})
		})

		s.It("has one result type per kind", func(ctx *specs.Context) {
			got := []domain.Kind{
				domain.IntakeResult{}.Kind(),
				domain.SpecResult{}.Kind(),
				domain.BuildResult{}.Kind(),
				domain.ReviewResult{}.Kind(),
				domain.ApproveResult{}.Kind(),
				domain.TaskResult{}.Kind(),
			}
			ctx.Expect(got).ToEqual(domain.Kinds)
		})
	})
}
