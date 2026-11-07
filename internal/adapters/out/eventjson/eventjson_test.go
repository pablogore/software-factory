package eventjson_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/eventjson"
	"github.com/pablogore/software-factory/internal/domain"
)

const jobID domain.JobID = "job-1"

func at(minute int) time.Time { return time.Date(2026, 11, 6, 20, minute, 0, 0, time.UTC) }

type sample struct {
	name string
	ev   domain.Event
}

// samples holds one event of every domain type, and ResultSubmitted once per
// result kind, with every field set.
func samples() []sample {
	id, ini := jobID, domain.InitiativeID("ini-1")
	t := at(1)
	return []sample{
		{"intake result", domain.ResultSubmitted{Job: id, Role: "intake", Result: domain.IntakeResult{Decision: domain.IntakeAccept, Slices: []domain.Slice{{ID: "S1", Title: "Save"}}, Reason: "fits"}, At: t}},
		{"spec result", domain.ResultSubmitted{Job: id, Role: "spec-writer", Result: domain.SpecResult{Title: "S1", Spec: "# S1"}, At: t}},
		{"build result", domain.ResultSubmitted{Job: id, Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady, Summary: "done"}, At: t}},
		{"review result", domain.ResultSubmitted{Job: id, Role: "reviewer-code", Result: domain.ReviewResult{Verdict: domain.ReviewChanges, Findings: []domain.Finding{{File: "a.go", Line: 3, Severity: "high", Category: "bug", Role: "reviewer-code", Message: "nil"}}}, At: t}},
		{"approve result", domain.ResultSubmitted{Job: id, Role: "approver", Result: domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"risky"}}, At: t}},
		{"task result", domain.ResultSubmitted{Job: id, Role: "docs", Result: domain.TaskResult{Produces: []string{"README.md"}, Verify: []domain.Check{{Name: "links", Passed: true}}}, At: t}},
		{"triage result", domain.ResultSubmitted{Job: id, Role: "tech-lead", Result: domain.TriageResult{Decision: domain.TriageEscalate, Brief: "rebase", Question: "Keep the flag? yes/no"}, At: t}},
		{"HumanApproved", domain.HumanApproved{Job: id, By: "ana", WaitSeq: 2, At: t}},
		{"SpecgatePassed", domain.SpecgatePassed{Job: id, At: t}},
		{"SpecgateFailed", domain.SpecgateFailed{Job: id, Exception: true, At: t}},
		{"SpecRejected", domain.SpecRejected{Job: id, Rejections: 2, At: t}},
		{"GatePassed", domain.GatePassed{Job: id, At: t}},
		{"GateFailed", domain.GateFailed{Job: id, At: t}},
		{"StageEntered", domain.StageEntered{Job: id, Stage: "review", At: t}},
		{"ReviewPassed", domain.ReviewPassed{Job: id, Role: "reviewer-code", At: t}},
		{"ChangesRequested", domain.ChangesRequested{Job: id, Role: "reviewer-code", Findings: []domain.Finding{{File: "b.go", Severity: "low"}}, At: t}},
		{"RoundStarted", domain.RoundStarted{Job: id, Round: 2, Reviewers: []string{"reviewer-code"}, At: t}},
		{"NeedsHuman", domain.NeedsHuman{Job: id, Reason: "last round", Question: "Send it back to build? yes/no", Cause: domain.HumanCauses()[0], Spent: 2.5, At: t}},
		{"TriageRequested", domain.TriageRequested{Job: id, Reason: "gate failed", Cause: domain.CauseGateFailed, At: t}},
		{"TriageRetried", domain.TriageRetried{Job: id, Role: "tech-lead", Brief: "rebase on the base first", At: t}},
		{"MergeFailed", domain.MergeFailed{Job: id, Reason: "conflict with the base", At: t}},
		{"BudgetExhausted", domain.BudgetExhausted{Job: id, Spent: 5.25, At: t}},
		{"ActionFailed", domain.ActionFailed{Job: id, Key: "job-1/comment/1", Kind: "comment", Error: "403 forbidden", At: t}},
		{"AttemptFailed", domain.AttemptFailed{Job: id, Role: "builder", Reason: "no result submitted", At: t}},
		{"NeedsInfo", domain.NeedsInfo{Job: id, From: "intake", At: t}},
		{"JobRejected", domain.JobRejected{Job: id, Reason: "duplicate", At: t}},
		{"MergeRequested", domain.MergeRequested{Job: id, At: t}},
		{"MergeCompleted", domain.MergeCompleted{Job: id, At: t}},
		{"MessageReceived", domain.MessageReceived{Job: id, Provider: "github", Thread: "29", Author: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Direction: true, Text: "go on", WaitSeq: 3, At: t}},
		{"ReworkRequested", domain.ReworkRequested{Job: id, By: "ana", Text: "fix it", At: t}},
		{"InfoAnswered", domain.InfoAnswered{Job: id, By: "ana", Text: "yes", At: t}},
		{"OverrideRequested", domain.OverrideRequested{Job: id, By: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Reason: "ship it", WaitSeq: 4, At: t}},
		{"MergeOverridden", domain.MergeOverridden{Job: id, By: "ana", Reason: "ship it", At: t}},
		{"LimitOverrideRequested", domain.LimitOverrideRequested{Job: id, By: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Limit: "human_reworks", Value: "unlimited", WaitSeq: 5, At: t}},
		{"LimitOverridden", domain.LimitOverridden{Job: id, By: "ana", Limit: "max_rounds", Value: 4, At: t}},
		{"CapRaiseRequested", domain.CapRaiseRequested{Job: id, By: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Cap: domain.CapBudget, Value: 7.5, WaitSeq: 6, At: t}},
		{"CapRaised", domain.CapRaised{Job: id, By: "ana", Cap: domain.CapSlices, Value: 7, At: t}},
		{"StopRequested", domain.StopRequested{Job: id, By: "ana", At: t}},
		{"JobStopped", domain.JobStopped{Job: id, By: "ana", At: t}},
		{"ReplanRequested", domain.ReplanRequested{Job: id, Initiative: ini, At: t}},
		{"SliceDropped", domain.SliceDropped{Job: id, Initiative: ini, By: "ana", Text: "drop it", At: t}},
		{"ReplanSubmitted", domain.ReplanSubmitted{Job: id, Initiative: ini, Status: domain.ReplanNeedsHuman, Reason: "unclear", At: t}},
		{"CheckpointReached", domain.CheckpointReached{Job: id, Initiative: ini, At: t}},
		{"NextSliceRequested", domain.NextSliceRequested{Job: id, Initiative: ini, At: t}},
		{"SliceStarted", domain.SliceStarted{Job: id, Initiative: ini, At: t}},
		{"InitiativeEnded", domain.InitiativeEnded{Job: id, Initiative: ini, At: t}},
		{"InitiativeNeedsHumanRaised", domain.InitiativeNeedsHumanRaised{Job: id, Initiative: ini, Reason: "unclear", At: t}},
		{"InitiativeAnswered", domain.InitiativeAnswered{Job: id, Initiative: ini, By: "ana", Text: "do S2", At: t}},
		{"AgentProgressed", domain.AgentProgressed{Job: id, Role: "builder", Kind: domain.ProgressEnded, Text: "read a.go", ToolCall: "call-1", ToolStatus: "completed", InputTokens: 120, OutputTokens: 40, StopReason: "end_turn", Error: "agent exited", At: t}},
	}
}

// domainEvents lists the types in internal/domain that implement EventName,
// so a new event the samples miss fails the contract.
func domainEvents() ([]string, error) {
	const dir = "../../../domain"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "EventName" {
				continue
			}
			if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				names = append(names, id.Name)
			}
		}
	}
	slices.Sort(names)
	return names, nil
}

// Both stores keep events as this encoding holds them, so reading every type
// back is a property of the encoding, not of either store.
func TestEventJSON(t *testing.T) {
	specs.Describe(t, "eventjson", func(s *specs.Spec) {
		s.It("the samples cover every domain event type", func(ctx *specs.Context) {
			want, err := domainEvents()
			ctx.Expect(err).To(specs.BeNil())
			var got []string
			for _, r := range samples() {
				got = append(got, r.ev.EventName())
			}
			slices.Sort(got)

			ctx.Expect(slices.Compact(got)).ToEqual(want)
		})

		specs.Table(s, samples(), func(r sample) string {
			return "decodes " + r.name + " back as the same type and value, with its date"
		}, func(ctx *specs.Context, r sample) {
			env, err := eventjson.Encode(r.ev)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(env.At).ToEqual(at(1))

			got, err := eventjson.Decode(env)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(r.ev)
		})
	})
}
