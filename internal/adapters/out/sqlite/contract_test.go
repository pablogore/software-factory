package sqlite_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/ports"
)

// store is what the contract exercises: both ports, as one adapter offers
// them, and Plant, which writes what no Apply would so the contract can read
// it back.
type store interface {
	ports.JobStore
	ports.EventLog
	Plant(ctx context.Context, job domain.Job, evs ...domain.Event) error
}

// otherEvent is not a domain event, so no store may save it.
type otherEvent struct{ Job domain.JobID }

func (otherEvent) EventName() string                   { return "OtherEvent" }
func (e otherEvent) JobID() domain.JobID               { return e.Job }
func at(minute int) time.Time                          { return time.Date(2026, 11, 6, 20, minute, 0, 0, time.UTC) }
func record(ev domain.Event, t time.Time) ports.Record { return ports.Record{Event: ev, At: t} }

const jobID domain.JobID = "job-1"

func newJob() domain.Job {
	return domain.Job{
		ID:       jobID,
		Title:    "Save jobs",
		Pipeline: "default",
		Stage:    "build",
		Round:    1,
		Origin:   domain.Origin{Provider: "github", Thread: "29", Author: "pablogore"},
	}
}

// pipeline is the flow the contract decides in: newJob starts in its build
// stage.
func pipeline() domain.Pipeline {
	return domain.Pipeline{
		Name: "default",
		Stages: []domain.Stage{
			{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
			{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code", "reviewer-security"}, OnChanges: "build"},
			{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
		},
	}
}

// built is the builder's result for the job: ready enters the gate, blocked
// sends it to needs-human.
func built(status domain.BuildStatus, minute int) domain.ResultSubmitted {
	return domain.ResultSubmitted{Job: jobID, Role: "builder", Result: domain.BuildResult{Status: status, Summary: "done"}, At: at(minute)}
}

// decided is what Decide returns for ev on job in pipeline, the events a
// store must save.
func decided(ctx *specs.Context, job domain.Job, ev domain.Event) []domain.Event {
	evs, err := decide.Decide(job, ev, pipeline())
	ctx.Expect(err).To(specs.BeNil())
	return evs
}

// created saves newJob in st and returns it as loaded, with its version.
func created(ctx *specs.Context, st store) (domain.Job, ports.Version) {
	ctx.Expect(st.Create(context.Background(), newJob())).To(specs.BeNil())
	job, v, err := st.Load(context.Background(), jobID)
	ctx.Expect(err).To(specs.BeNil())
	return job, v
}

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
		{"NeedsHuman", domain.NeedsHuman{Job: id, Reason: "last round", Cause: domain.HumanCauses()[0], At: t}},
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

// storeContract is the behavior every JobStore and EventLog shares: the
// SQLite store and the in-memory fake run it alike.
func storeContract(s *specs.Spec, newStore func(ctx *specs.Context) store) {
	bg := context.Background()

	s.When("a job is created", func(s *specs.Spec) {
		s.It("loads it back as created, with an empty history", func(ctx *specs.Context) {
			st := newStore(ctx)
			ctx.Expect(st.Create(bg, newJob())).To(specs.BeNil())

			got, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(newJob())

			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
		})

		s.It("refuses to create it again", func(ctx *specs.Context) {
			st := newStore(ctx)
			ctx.Expect(st.Create(bg, newJob())).To(specs.BeNil())

			ctx.Expect(st.Create(bg, newJob())).To(specs.MatchError(ports.ErrJobExists))
		})
	})

	s.When("the job does not exist", func(s *specs.Spec) {
		s.It("load, apply and history fail with job not found", func(ctx *specs.Context) {
			st := newStore(ctx)

			_, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.MatchError(ports.ErrJobNotFound))
			_, _, err = st.Apply(bg, jobID, 1, pipeline(), built(domain.BuildReady, 1))
			ctx.Expect(err).To(specs.MatchError(ports.ErrJobNotFound))
			_, err = st.History(bg, jobID)
			ctx.Expect(err).To(specs.MatchError(ports.ErrJobNotFound))
		})
	})

	s.When("an event is applied", func(s *specs.Spec) {
		s.It("saves the state folded from Decide's events and lists them in saved order with their dates", func(ctx *specs.Context) {
			st := newStore(ctx)
			job, v := created(ctx, st)
			review := domain.ResultSubmitted{Job: jobID, Role: "reviewer-code", Result: domain.ReviewResult{Verdict: domain.ReviewPass}, At: at(3)}

			evs, v, err := st.Apply(bg, jobID, v, pipeline(), built(domain.BuildReady, 1))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.StageEntered{Job: jobID, Stage: domain.StageGate, At: at(1)}})
			_, v, err = st.Apply(bg, jobID, v, pipeline(), domain.GatePassed{Job: jobID, At: at(2)})
			ctx.Expect(err).To(specs.BeNil())
			_, v, err = st.Apply(bg, jobID, v, pipeline(), review)
			ctx.Expect(err).To(specs.BeNil())

			got, gotV, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(gotV).ToEqual(v)
			ctx.Expect(got.Stage).ToEqual("review")
			ctx.Expect(got.Passed).ToEqual([]string{"reviewer-code"})
			want := decide.Apply(job, []domain.Event{
				domain.StageEntered{Job: jobID, Stage: domain.StageGate, At: at(1)},
				domain.StageEntered{Job: jobID, Stage: "review", At: at(2)},
				domain.ReviewPassed{Job: jobID, Role: "reviewer-code", At: at(3)},
			})
			ctx.Expect(got).ToEqual(want)

			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{
				record(domain.StageEntered{Job: jobID, Stage: domain.StageGate, At: at(1)}, at(1)),
				record(domain.StageEntered{Job: jobID, Stage: "review", At: at(2)}, at(2)),
				record(domain.ReviewPassed{Job: jobID, Role: "reviewer-code", At: at(3)}, at(3)),
			})
		})

		s.It("saves every event Decide returns together, as MergeOverridden and MergeRequested on an override", func(ctx *specs.Context) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			waiting := newJob()
			waiting.Stage, waiting.WaitSeq, waiting.Cause, waiting.Reason = domain.StageNeedsHuman, 1, domain.CauseChanges, "changes"
			ctx.Expect(st.Plant(bg, waiting)).To(specs.BeNil())
			override := domain.OverrideRequested{Job: jobID, By: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Reason: "ship it", WaitSeq: 1, At: at(1)}
			want := decided(ctx, waiting, override)
			ctx.Expect(want).To(specs.HaveLen(2))

			evs, _, err := st.Apply(bg, jobID, v, pipeline(), override)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual(want)
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(want[0], at(1)), record(want[1], at(1))})
		})

		s.It("an event Decide ignores saves nothing and keeps the version", func(ctx *specs.Context) {
			st := newStore(ctx)
			job, v := created(ctx, st)
			bot := domain.MessageReceived{Job: jobID, Provider: "github", Thread: "29", Author: "ci", AuthorKind: domain.AuthorBot, Text: "hi", WaitSeq: 1, At: at(1)}

			evs, got, err := st.Apply(bg, jobID, v, pipeline(), bot)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).To(specs.BeEmpty())
			ctx.Expect(got).ToEqual(v)
			loaded, gotV, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(loaded).ToEqual(job)
			ctx.Expect(gotV).ToEqual(v)
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
		})

		s.It("keeps each job's history apart", func(ctx *specs.Context) {
			st := newStore(ctx)
			other := newJob()
			other.ID = "job-2"
			ctx.Expect(st.Create(bg, newJob())).To(specs.BeNil())
			ctx.Expect(st.Create(bg, other)).To(specs.BeNil())
			_, v, err := st.Load(bg, other.ID)
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = st.Apply(bg, other.ID, v, pipeline(), domain.ResultSubmitted{Job: other.ID, Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at(1)})
			ctx.Expect(err).To(specs.BeNil())

			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
			hist, err = st.History(bg, other.ID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(domain.StageEntered{Job: other.ID, Stage: domain.StageGate, At: at(1)}, at(1))})
		})
	})

	s.When("Decide refuses the event", func(s *specs.Spec) {
		rows := []refusedRow{
			{"an unknown event type", otherEvent{Job: jobID}, decide.ErrIllegalTransition},
			{"a nil event", nil, decide.ErrIllegalTransition},
			{"a ResultSubmitted without a result", domain.ResultSubmitted{Job: jobID, Role: "builder", At: at(2)}, decide.ErrIllegalTransition},
			{"an event Decide only returns, never takes", domain.StageEntered{Job: jobID, Stage: domain.StageMerge, At: at(2)}, decide.ErrIllegalTransition},
			{"an event for another job", domain.ResultSubmitted{Job: "job-2", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at(2)}, decide.ErrOtherJob},
		}
		specs.Table(s, rows, func(r refusedRow) string {
			return r.name + " is refused and saves neither the state nor any event"
		}, func(ctx *specs.Context, r refusedRow) {
			st := newStore(ctx)
			_, v := created(ctx, st)
			_, v, err := st.Apply(bg, jobID, v, pipeline(), built(domain.BuildReady, 1))
			ctx.Expect(err).To(specs.BeNil())
			before, _, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())

			_, _, err = st.Apply(bg, jobID, v, pipeline(), r.ev)

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			got, gotV, err := st.Load(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(got).ToEqual(before)
			ctx.Expect(gotV).ToEqual(v)
			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(domain.StageEntered{Job: jobID, Stage: domain.StageGate, At: at(1)}, at(1))})
		})
	})

	s.When("every event type is read back", func(s *specs.Spec) {
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

		// Apply saves only what Decide returns, and Decide returns few of
		// these types, so the events are planted: what is read back is what
		// the store's encoding holds.
		specs.Table(s, samples(), func(r sample) string {
			return "reads " + r.name + " back as the same type and value"
		}, func(ctx *specs.Context, r sample) {
			st := newStore(ctx)
			ctx.Expect(st.Plant(bg, newJob(), r.ev)).To(specs.BeNil())

			hist, err := st.History(bg, jobID)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).ToEqual([]ports.Record{record(r.ev, at(1))})
		})
	})
}

type refusedRow struct {
	name     string
	ev       domain.Event
	sentinel error
}
