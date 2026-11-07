package build_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/build"
)

var at = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

type stopped struct{}

func (stopped) Now() time.Time { return at }

type pipelines struct{}

func (pipelines) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if name != "default" {
		return domain.Pipeline{}, fmt.Errorf("no pipeline %q", name)
	}
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
	}}, nil
}

// worktrees puts every job's worktree under /wt.
type worktrees struct{}

func (worktrees) Prepare(_ context.Context, job domain.JobID) (ports.Worktree, error) {
	return ports.Worktree{Job: job, Path: "/wt/" + string(job), Branch: "factory/" + string(job)}, nil
}

// checks answers every run with run, or with err, and records where and
// what it was asked to run.
type checks struct {
	run     domain.ChecksRun
	err     error
	dir     string
	command []string
	calls   int
}

func (c *checks) Run(_ context.Context, dir string, command []string) (domain.ChecksRun, error) {
	c.calls++
	c.dir, c.command = dir, command
	return c.run, c.err
}

var (
	passing = domain.ChecksRun{Command: "make check", Duration: 2 * time.Second, Output: "ok"}
	failing = domain.ChecksRun{Command: "make check", ExitCode: 2, Duration: 3 * time.Second, Output: "--- FAIL: TestSave"}
)

func newGate(ctx *specs.Context, stage string, round int, c *checks) (*build.Gate, *memstore.Store) {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	job := domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: stage, Round: round}
	ctx.Expect(store.Create(context.Background(), job)).To(specs.BeNil())
	return &build.Gate{Jobs: store, Pipelines: pipelines{}, Worktrees: worktrees{}, Checks: c, Clock: stopped{}, Command: build.DefaultCommand}, store
}

func names(evs []domain.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.EventName()
	}
	return out
}

func TestGate(t *testing.T) {
	specs.Describe(t, "build.Gate", func(s *specs.Spec) {
		bg := context.Background()

		s.It("runs make check by default in the job's worktree, with no agent", func(ctx *specs.Context) {
			c := &checks{run: passing}
			g, _ := newGate(ctx, domain.StageGate, 1, c)

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.dir).ToEqual("/wt/j-1")
			ctx.Expect(c.command).ToEqual([]string{"make", "check"})
		})

		s.It("runs the configured command instead", func(ctx *specs.Context) {
			c := &checks{run: passing}
			g, _ := newGate(ctx, domain.StageGate, 1, c)
			g.Command = []string{"go", "test", "./..."}

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.command).ToEqual([]string{"go", "test", "./..."})
		})

		s.When("the checks pass", func(s *specs.Spec) {
			s.It("keeps the run with the job and moves it on to review", func(ctx *specs.Context) {
				g, store := newGate(ctx, domain.StageGate, 1, &checks{run: passing})

				evs, err := g.Run(bg, "j-1")

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{
					domain.ChecksRan{Job: "j-1", Round: 1, Run: passing, At: at},
					domain.StageEntered{Job: "j-1", Stage: "review", At: at},
				})
				job, _, err := store.Load(bg, "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Stage).ToEqual("review")
			})
		})

		s.When("the checks fail with a round left", func(s *specs.Spec) {
			s.It("sends the job back to build in a new round, with no reviewer started", func(ctx *specs.Context) {
				g, store := newGate(ctx, domain.StageGate, 1, &checks{run: failing})

				evs, err := g.Run(bg, "j-1")

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(names(evs)).ToEqual([]string{"ChecksRan", "RoundStarted", "StageEntered"})
				job, _, err := store.Load(bg, "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Stage).ToEqual("build")
				ctx.Expect(job.Round).ToEqual(2)
				ctx.Expect(job.Passed).To(specs.BeEmpty())
				ctx.Expect(job.Changes).To(specs.BeEmpty())
			})

			s.It("leaves the log, its exit code and duration for the builder of the new round", func(ctx *specs.Context) {
				g, store := newGate(ctx, domain.StageGate, 1, &checks{run: failing})
				_, err := g.Run(bg, "j-1")
				ctx.Expect(err).To(specs.BeNil())

				hist, err := store.History(bg, "j-1")
				ctx.Expect(err).To(specs.BeNil())
				last, ok := build.LastChecks(hist)

				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(last).ToEqual(domain.ChecksRan{Job: "j-1", Round: 1, Run: failing, At: at})
			})
		})

		s.It("sends a job whose checks fail in the last round to triage, keeping the run", func(ctx *specs.Context) {
			g, store := newGate(ctx, domain.StageGate, domain.DefaultMaxRounds, &checks{run: failing})

			evs, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(names(evs)).ToEqual([]string{"ChecksRan", "TriageRequested"})
			job, _, err := store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual(domain.StageTriage)
		})

		s.It("runs nothing for a job that is not at the gate", func(ctx *specs.Context) {
			c := &checks{run: passing}
			g, store := newGate(ctx, "build", 1, c)

			evs, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(build.ErrNotAtGate))
			ctx.Expect(evs).To(specs.BeEmpty())
			ctx.Expect(c.calls).ToEqual(0)
			hist, err := store.History(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
		})

		s.It("saves nothing when the checks cannot be run", func(ctx *specs.Context) {
			broken := errors.New("make: not found")
			g, store := newGate(ctx, domain.StageGate, 1, &checks{err: broken})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(broken))
			job, _, err := store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual(domain.StageGate)
		})

		s.It("refuses to run with no checks command", func(ctx *specs.Context) {
			c := &checks{run: passing}
			g, _ := newGate(ctx, domain.StageGate, 1, c)
			g.Command = nil

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(c.calls).ToEqual(0)
		})
	})

	specs.Describe(t, "build.LastChecks", func(s *specs.Spec) {
		s.It("is the latest run in the history", func(ctx *specs.Context) {
			first := domain.ChecksRan{Job: "j-1", Round: 1, Run: failing, At: at}
			second := domain.ChecksRan{Job: "j-1", Round: 2, Run: passing, At: at.Add(time.Minute)}
			hist := []ports.Record{
				{Event: first, At: first.At},
				{Event: domain.StageEntered{Job: "j-1", Stage: "build", At: at}, At: at},
				{Event: second, At: second.At},
				{Event: domain.StageEntered{Job: "j-1", Stage: "review", At: second.At}, At: second.At},
			}

			last, ok := build.LastChecks(hist)

			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(last).ToEqual(second)
		})

		s.It("is nothing before the gate ever ran", func(ctx *specs.Context) {
			_, ok := build.LastChecks([]ports.Record{{Event: domain.StageEntered{Job: "j-1", Stage: "build", At: at}, At: at}})

			ctx.Expect(ok).To(specs.BeFalse())
		})
	})
}
