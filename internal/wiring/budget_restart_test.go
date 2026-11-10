package wiring_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/worker"
	"github.com/pablogore/software-factory/internal/wiring"
)

var errSessionGone = errors.New("session gone")

// sessionCost is what each session the fake agents start spends.
const sessionCost = 0.5

// spend is what each job spent, kept across restarts as factoryd's cost
// record would be.
type spend struct {
	mu  sync.Mutex
	usd map[domain.JobID]float64
}

func (s *spend) add(job domain.JobID, usd float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usd[job] += usd
}

func (s *spend) of(job domain.JobID) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usd[job]
}

// storeCosts is the scheduler's cost source: spend's spend and the job's own
// budget as the store keeps it.
type storeCosts struct {
	spent *spend
	jobs  ports.JobStore
}

func (c storeCosts) Spent(_ context.Context, job domain.JobID) (float64, error) {
	return c.spent.of(job), nil
}

func (c storeCosts) Budget(ctx context.Context, job domain.JobID) (float64, error) {
	j, _, err := c.jobs.Load(ctx, job)
	return j.Budget, err
}

// spendingAgents count the sessions started, each spending sessionCost; a
// session never comes up, so its attempt fails.
type spendingAgents struct {
	spent *spend

	mu     sync.Mutex
	starts []string
}

func (a *spendingAgents) Start(_ context.Context, role domain.Role, s ports.SessionSpec) (ports.Session, error) {
	a.mu.Lock()
	a.starts = append(a.starts, role.Name)
	a.mu.Unlock()
	a.spent.add(s.Job, sessionCost)
	return nil, errSessionGone
}

func (a *spendingAgents) started() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.starts)
}

type launchPipelines struct{}

func (launchPipelines) Pipeline(context.Context, string) (domain.Pipeline, error) {
	return launchPipeline, nil
}

type buildRoles struct{}

func (buildRoles) Role(_ context.Context, name string) (domain.Role, string, error) {
	return domain.Role{Name: name, Kind: domain.KindBuild}, "prompt of " + name, nil
}

type tempWorktrees struct{ dir string }

func (w tempWorktrees) Prepare(_ context.Context, job domain.JobID) (ports.Worktree, error) {
	return ports.Worktree{Job: job, Path: w.dir}, nil
}

// budgetRun is one life of factoryd's launches on the store at path, with
// its own scheduler and bus, as a restart builds them again.
type budgetRun struct {
	store    *sqlite.Store
	sched    *scheduler.Scheduler
	launcher *worker.Launcher
	bus      *bus.Bus
}

func startBudgetRun(ctx *specs.Context, path string, spent *spend, agents *spendingAgents) budgetRun {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	sched, err := scheduler.New(1, 5, storeCosts{spent: spent, jobs: st}, scheduler.GitHub{Limits: plentyLimit{}, Threshold: 100, Clock: stillClock{}})
	ctx.Expect(err).To(specs.BeNil())
	r := budgetRun{store: st, sched: sched, bus: bus.New(st, stillClock{})}
	r.launcher = &worker.Launcher{
		Tokens: jobtoken.New(), Jobs: st, Pipelines: launchPipelines{}, Roles: buildRoles{}, Agents: agents,
		Worktrees: tempWorktrees{ctx.Testing().TempDir()}, Scheduler: sched,
		Attempts: &worker.Attempts{Jobs: st, Pipelines: launchPipelines{}, Clock: stillClock{}},
		MCP:      func(job domain.JobID) string { return "http://factoryd/" + string(job) }, Log: &syncBuffer{},
		Timeout: time.Hour, Clock: stillClock{},
	}
	ctx.Expect(r.bus.Subscribe(wiring.LaunchSubscriber, r.launcher.Launch, worker.LaunchEvents...)).To(specs.BeNil())
	ctx.Expect(r.bus.Start(bg)).To(specs.BeNil())
	return r
}

func (r budgetRun) job(ctx *specs.Context, id domain.JobID) domain.Job {
	j, _, err := r.store.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	return j
}

// until fails the test unless ok holds within brakePatience.
func until(ctx *specs.Context, ok func() bool) {
	deadline := time.Now().Add(brakePatience)
	for !ok() {
		if time.Now().After(deadline) {
			ctx.Testing().Fatal("it never happened")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDemoBudgetStopSurvivesRestart(t *testing.T) {
	specs.Describe(t, "a job stopped for its spent budget across a restart of factoryd", func(s *specs.Spec) {
		bg := context.Background()

		s.It("still waits for a person after a restart, launches and spends nothing, and resumes where it stopped once its budget is raised", func(ctx *specs.Context) {
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			seedBuild(ctx, path, "j-1")
			spent := &spend{usd: map[domain.JobID]float64{"j-1": 4.5}}
			agents := &spendingAgents{spent: spent}

			first := startBudgetRun(ctx, path, spent, agents)
			until(ctx, func() bool { return agents.started() == 1 && first.job(ctx, "j-1").AttemptsFailed == 1 })
			ctx.Expect(spent.of("j-1")).ToEqual(5.0)
			ctx.Expect(first.bus.Dispatch(bg)).To(specs.BeNil())
			until(ctx, func() bool { return first.job(ctx, "j-1").Stage == domain.StageNeedsHuman })
			ctx.Expect(agents.started()).ToEqual(1)
			ctx.Expect(first.store.Close()).To(specs.BeNil())

			second := startBudgetRun(ctx, path, spent, agents)
			ctx.Cleanup(func() { _ = second.store.Close() })
			stopped := second.job(ctx, "j-1")
			ctx.Expect(stopped.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(stopped.Cause).ToEqual(domain.CauseBudget)
			ctx.Expect(stopped.Reason).ToEqual("budget exhausted")
			ctx.Expect(stopped.Paused).ToEqual(&domain.Paused{Stage: "build", Spent: 5})

			ctx.Expect(second.bus.Dispatch(bg)).To(specs.BeNil())
			hist, err := second.store.History(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			for _, rec := range hist {
				// Every launch event handed over again, as an at-least-once
				// bus may after a restart.
				ctx.Expect(second.launcher.Launch(bg, rec.Event)).To(specs.BeNil())
			}
			time.Sleep(100 * time.Millisecond)
			ctx.Expect(agents.started()).ToEqual(1)
			ctx.Expect(spent.of("j-1")).ToEqual(5.0)
			ctx.Expect(second.sched.Running()).ToEqual(0)
			ctx.Expect(second.sched.Waiting()).ToEqual(0)
			ctx.Expect(second.job(ctx, "j-1").Stage).ToEqual(domain.StageNeedsHuman)

			_, v, err := second.store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			raise := domain.CapRaiseRequested{Job: "j-1", By: "operator", AuthorKind: domain.AuthorPerson, Allowed: true, Cap: domain.CapBudget, Value: 8, WaitSeq: stopped.WaitSeq, At: brakeEpoch}
			_, _, err = second.store.Apply(bg, "j-1", v, launchPipeline, raise)
			ctx.Expect(err).To(specs.BeNil())
			resumed := second.job(ctx, "j-1")
			ctx.Expect(resumed.Stage).ToEqual("build")
			ctx.Expect(resumed.Round).ToEqual(stopped.Round)
			ctx.Expect(resumed.Budget).ToEqual(8.0)

			ctx.Expect(second.bus.Dispatch(bg)).To(specs.BeNil())
			until(ctx, func() bool { return agents.started() == 2 && second.sched.Running() == 0 })
			ctx.Expect(agents.starts).ToEqual([]string{"builder", "builder"})
			ctx.Expect(spent.of("j-1")).ToEqual(5.5)
		})
	})
}
