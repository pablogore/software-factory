package wiring_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/runmode"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
)

// brakePatience bounds how long the demo waits for an issue to start, so a
// broken brake fails instead of hanging.
const brakePatience = 2 * time.Second

var brakeEpoch = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// issueRunner is a fake queue.Runner: each issue's work tells it started
// and waits until the test finishes it.
type issueRunner struct {
	mu      sync.Mutex
	started map[int]chan struct{}
	finish  map[int]chan struct{}
}

func newIssueRunner() *issueRunner {
	return &issueRunner{started: map[int]chan struct{}{}, finish: map[int]chan struct{}{}}
}

func (r *issueRunner) chans(n int) (chan struct{}, chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started[n] == nil {
		r.started[n], r.finish[n] = make(chan struct{}), make(chan struct{})
	}
	return r.started[n], r.finish[n]
}

func (r *issueRunner) Work(is queue.Issue) (domain.JobID, scheduler.Work) {
	started, finish := r.chans(is.Number)
	return domain.JobID(fmt.Sprint(is.Number)), func(context.Context) error {
		close(started)
		<-finish
		return nil
	}
}

func (*issueRunner) Done(queue.Issue, error) {}

// starts tells whether issue n starts within wait.
func (r *issueRunner) starts(n int, wait time.Duration) bool {
	started, _ := r.chans(n)
	select {
	case <-started:
		return true
	case <-time.After(wait):
		return false
	}
}

func (r *issueRunner) end(n int) {
	_, finish := r.chans(n)
	close(finish)
}

type noCosts struct{}

func (noCosts) Spent(context.Context, domain.JobID) (float64, error)  { return 0, nil }
func (noCosts) Budget(context.Context, domain.JobID) (float64, error) { return 0, nil }

type plentyLimit struct{}

func (plentyLimit) Limit(context.Context) (scheduler.RateLimit, error) {
	return scheduler.RateLimit{Remaining: 5000, Reset: brakeEpoch.Add(time.Hour)}, nil
}

type stillClock struct{}

func (stillClock) Now() time.Time                       { return brakeEpoch }
func (stillClock) After(time.Duration) <-chan time.Time { return nil }

// openIssues is a fake queue.Issues: no issue depends on another.
type openIssues struct{}

func (openIssues) Finished(_ context.Context, n int) (bool, error) {
	return false, fmt.Errorf("issue #%d: %w", n, queue.ErrNoIssue)
}

// doneAlone is a fake runmode.Reports: every job is the issue of its own
// number, done on its own.
type doneAlone struct{}

func (doneAlone) Report(_ context.Context, job domain.JobID) (runmode.IssueReport, error) {
	var n int
	if _, err := fmt.Sscan(string(job), &n); err != nil {
		return runmode.IssueReport{}, ports.ErrJobNotFound
	}
	return runmode.IssueReport{
		Issue: queue.Issue{Number: n},
		Jobs:  []runmode.JobReport{{Job: domain.Job{ID: job, Stage: domain.StageDone, Round: 1}, Start: brakeEpoch, End: brakeEpoch.Add(time.Minute)}},
	}, nil
}

// summaries is a fake runmode.Outbox and runmode.Inbox: it counts the
// summaries a run published.
type summaries struct {
	mu   sync.Mutex
	keys []string
}

func (s *summaries) Enqueue(_ context.Context, _ domain.JobID, acts ...ports.Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range acts {
		s.keys = append(s.keys, a.Key)
	}
	return nil
}

func (s *summaries) Put(context.Context, string, runmode.Summary) error { return nil }

func (s *summaries) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}

// factoryRun is one life of factoryd's run mode on the store at path, as a
// restart builds it again.
type factoryRun struct {
	store   *sqlite.Store
	runner  *issueRunner
	sched   *scheduler.Scheduler
	brake   *runmode.Brake
	queue   *queue.Queue
	mode    *runmode.Mode
	summary *summaries
}

func startRun(ctx *specs.Context, path string) factoryRun {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	sched, err := scheduler.New(1, 5, noCosts{}, scheduler.GitHub{Limits: plentyLimit{}, Threshold: 100, Clock: stillClock{}})
	ctx.Expect(err).To(specs.BeNil())
	f := factoryRun{store: st, runner: newIssueRunner(), sched: sched, summary: &summaries{}}
	f.brake = runmode.NewBrake(sched)
	f.queue = queue.New(f.brake, queue.Order{}, openIssues{}, f.runner)
	f.mode, err = runmode.New(bg, f.brake, f.queue, queue.Order{}, doneAlone{}, f.summary, f.summary, st)
	ctx.Expect(err).To(specs.BeNil())
	return f
}

func (f factoryRun) stop(ctx *specs.Context) {
	ctx.Expect(f.store.Close()).To(specs.BeNil())
}

func TestBrakeSurvivesRestart(t *testing.T) {
	specs.Describe(t, "the brake between issues across a restart of factoryd", func(s *specs.Spec) {
		bg := context.Background()

		s.It("starts on after a restart, starts no new issue until released, and stays off once released", func(ctx *specs.Context) {
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")

			first := startRun(ctx, path)
			first.queue.Add(bg, queue.Issue{Number: 1})
			ctx.Expect(first.runner.starts(1, brakePatience)).To(specs.BeTrue())
			ctx.Expect(first.mode.Handle(bg, domain.StageEntered{Job: "1", Stage: domain.StageDone, At: brakeEpoch})).To(specs.BeNil())
			ctx.Expect(first.brake.Engaged()).To(specs.BeTrue())
			ctx.Expect(first.summary.count()).ToEqual(1)
			first.runner.end(1)
			first.stop(ctx)

			second := startRun(ctx, path)
			ctx.Expect(second.brake.Engaged()).To(specs.BeTrue())
			second.queue.Add(bg, queue.Issue{Number: 2})
			ctx.Expect(second.runner.starts(2, 100*time.Millisecond)).To(specs.BeFalse())
			ctx.Expect(second.sched.Waiting()).ToEqual(0)
			ctx.Expect(second.summary.count()).ToEqual(0)

			ctx.Expect(second.mode.Release(bg)).To(specs.BeNil())
			ctx.Expect(second.runner.starts(2, brakePatience)).To(specs.BeTrue())
			second.runner.end(2)
			second.stop(ctx)

			third := startRun(ctx, path)
			ctx.Cleanup(func() { _ = third.store.Close() })
			ctx.Expect(third.brake.Engaged()).To(specs.BeFalse())
			third.queue.Add(bg, queue.Issue{Number: 3})
			ctx.Expect(third.runner.starts(3, brakePatience)).To(specs.BeTrue())
			third.runner.end(3)
		})

		s.It("starts off after a restart when it was never on, and the queue hands issues over", func(ctx *specs.Context) {
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			startRun(ctx, path).stop(ctx)

			again := startRun(ctx, path)
			ctx.Cleanup(func() { _ = again.store.Close() })
			ctx.Expect(again.brake.Engaged()).To(specs.BeFalse())
			again.queue.Add(bg, queue.Issue{Number: 1})
			ctx.Expect(again.runner.starts(1, brakePatience)).To(specs.BeTrue())
			again.runner.end(1)
		})
	})
}
