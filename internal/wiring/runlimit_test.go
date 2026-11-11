//go:build unix

package wiring_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/wiring"
	"github.com/pablogore/software-factory/pkg/worker"
)

// gitSteps is the git a gate runs, counting every step it takes.
type gitSteps interface {
	Changes(ctx context.Context, job domain.JobID, dir, base string) ([]domain.FileChange, error)
	Lines(ctx context.Context, job domain.JobID, dir, base string) (int, error)
	Flagged(ctx context.Context, job domain.JobID, dir string) ([]domain.FlaggedFile, error)
	AtBase(ctx context.Context, job domain.JobID, dir, base, file string) (string, error)
	InWorktree(ctx context.Context, job domain.JobID, dir, file string) (string, error)
}

type countingGit struct {
	gitSteps
	steps *atomic.Int64
}

func (c countingGit) Changes(ctx context.Context, job domain.JobID, dir, base string) ([]domain.FileChange, error) {
	c.steps.Add(1)
	return c.gitSteps.Changes(ctx, job, dir, base)
}

func (c countingGit) Lines(ctx context.Context, job domain.JobID, dir, base string) (int, error) {
	c.steps.Add(1)
	return c.gitSteps.Lines(ctx, job, dir, base)
}

func (c countingGit) Flagged(ctx context.Context, job domain.JobID, dir string) ([]domain.FlaggedFile, error) {
	c.steps.Add(1)
	return c.gitSteps.Flagged(ctx, job, dir)
}

func (c countingGit) AtBase(ctx context.Context, job domain.JobID, dir, base, file string) (string, error) {
	c.steps.Add(1)
	return c.gitSteps.AtBase(ctx, job, dir, base, file)
}

func (c countingGit) InWorktree(ctx context.Context, job domain.JobID, dir, file string) (string, error) {
	c.steps.Add(1)
	return c.gitSteps.InWorktree(ctx, job, dir, file)
}

// limitedGate is the gate run_gate runs over jobs j-1 to j-<jobs>, all in
// build on one fresh git repo, letting maxRuns calls run at once. Its checks
// write a line to calls and its test count one to counts, outside the
// worktree, and then wait until release exists; git counts its git steps.
type limitedGate struct {
	gate    *wiring.RunGateChecks
	store   *memstore.Store
	calls   string
	counts  string
	release string
	git     *atomic.Int64
}

func newLimitedGate(ctx *specs.Context, jobs, maxRuns int, timeout time.Duration) limitedGate {
	dir, base := gitRepo(ctx)
	tmp := ctx.Testing().TempDir()
	l := limitedGate{calls: filepath.Join(tmp, "calls"), counts: filepath.Join(tmp, "counts"), release: filepath.Join(tmp, "release"), git: &atomic.Int64{}}
	var err error
	l.store, err = memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	for i := 1; i <= jobs; i++ {
		id := domain.JobID("j-" + strconv.Itoa(i))
		ctx.Expect(l.store.Create(context.Background(), domain.Job{ID: id, Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
		ctx.Expect(l.store.RecordBase(context.Background(), id, base)).To(specs.BeNil())
	}
	checks := []string{"sh", "-c", "echo run >> " + l.calls + "; while [ ! -f " + l.release + " ]; do sleep 0.02; done"}
	count := []string{"sh", "-c", "echo count >> " + l.counts}
	l.gate = wiring.RunGate(l.store, onePipeline{}, oneDir(dir), l.store, fixedClock{}, gatelock.New(), childenv.Homes{Root: ctx.Testing().TempDir()}, checks, count, timeout, wiring.DefaultRunGateMaxOutput, maxRuns)
	l.gate.Diffs = countingGit{gitSteps: l.gate.Diffs, steps: l.git}
	return l
}

// lines is how many lines file has, 0 when it doesn't exist.
func lines(file string) int {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// waitRuns waits until n calls started their checks.
func (l limitedGate) waitRuns(ctx *specs.Context, n int) {
	for deadline := time.Now().Add(10 * time.Second); lines(l.calls) < n; time.Sleep(10 * time.Millisecond) {
		ctx.Expect(time.Now().Before(deadline)).To(specs.BeTrue())
	}
}

// start runs l's check of job in the background, its answer on the channel.
func (l limitedGate) start(job domain.JobID) chan error {
	done := make(chan error, 1)
	go func() {
		_, err := l.gate.Check(context.Background(), job)
		done <- err
	}()
	return done
}

func (l limitedGate) releaseRuns(ctx *specs.Context) {
	ctx.Expect(os.WriteFile(l.release, nil, 0o600)).To(specs.BeNil())
}

func TestRunGateMaxRuns(t *testing.T) {
	specs.Describe(t, "the gate run_gate runs, at its limit of calls at once", func(s *specs.Spec) {
		s.It("refuses one more call at once with a too many error, running no check, test count or git step", func(ctx *specs.Context) {
			l := newLimitedGate(ctx, 2, 1, time.Minute)
			first := l.start("j-1")
			l.waitRuns(ctx, 1)
			gitBefore, countsBefore := l.git.Load(), lines(l.counts)

			start := time.Now()
			run, err := l.gate.Check(context.Background(), "j-2")
			ctx.Expect(err).To(specs.MatchError(domain.ErrTooMany))
			ctx.Expect(err.Error()).ToEqual("too many running: 1 run_gate calls are running, as many as may run at once; nothing ran, so call again once one ends")
			ctx.Expect(run).ToEqual(domain.ChecksRun{})
			ctx.Expect(time.Since(start) < time.Second).To(specs.BeTrue())
			ctx.Expect(lines(l.calls)).ToEqual(1)
			ctx.Expect(lines(l.counts)).ToEqual(countsBefore)
			ctx.Expect(l.git.Load()).ToEqual(gitBefore)

			l.releaseRuns(ctx)
			ctx.Expect(<-first).To(specs.BeNil())
		})

		s.It("lets 4 calls run at once when no limit is set", func(ctx *specs.Context) {
			l := newLimitedGate(ctx, 5, 0, time.Minute)
			var running []chan error
			for _, job := range []domain.JobID{"j-1", "j-2", "j-3", "j-4"} {
				running = append(running, l.start(job))
			}
			l.waitRuns(ctx, 4)
			_, err := l.gate.Check(context.Background(), "j-5")
			ctx.Expect(err).To(specs.MatchError(domain.ErrTooMany))
			ctx.Expect(wiring.DefaultRunGateMaxRuns).ToEqual(4)
			l.releaseRuns(ctx)
			for _, done := range running {
				ctx.Expect(<-done).To(specs.BeNil())
			}
		})

		s.It("frees the place of a call cut by its timeout", func(ctx *specs.Context) {
			l := newLimitedGate(ctx, 2, 1, 300*time.Millisecond)
			_, err := l.gate.Check(context.Background(), "j-1")
			ctx.Expect(err).To(specs.MatchError(domain.ErrTimedOut))
			// The next call has a deadline no load reaches, so only a place
			// still held can fail it.
			l.gate.Timeout = time.Minute
			l.releaseRuns(ctx)
			run, err := l.gate.Check(context.Background(), "j-2")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Passed()).To(specs.BeTrue())
		})

		s.It("frees the place of a call that ended in an error", func(ctx *specs.Context) {
			l := newLimitedGate(ctx, 1, 1, time.Minute)
			_, err := l.gate.Check(context.Background(), "no-such-job")
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(errors.Is(err, domain.ErrTooMany)).To(specs.BeFalse())
			l.releaseRuns(ctx)
			run, err := l.gate.Check(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Passed()).To(specs.BeTrue())
		})
	})
}

// TestDemoRunGateMaxRuns is the slice's demo: through the worker API, with
// a limit of 1 call at once, a call on j-2 while a slow one on j-1 runs is
// refused without running anything, and once the slow one ends a third
// call, on j-2 again, runs normally.
func TestDemoRunGateMaxRuns(t *testing.T) {
	specs.Describe(t, "run_gate with 1 call at once", func(s *specs.Spec) {
		s.It("refuses a second call while a slow one runs, and runs the next once it ends", func(ctx *specs.Context) {
			l := newLimitedGate(ctx, 2, 1, time.Minute)
			tokens := jobtoken.New()
			srv := httptest.NewServer(wiring.WorkerAPI(tokens, l.store, onePipeline{}, l.gate, l.gate.Locks.(*gatelock.Locks), fixedClock{}, nil, nil))
			ctx.Cleanup(srv.Close)
			client := func(job domain.JobID) worker.Client {
				token, err := tokens.Issue(jobtoken.Grant{Job: job, Role: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress", "run_gate"}, Round: 1})
				ctx.Expect(err).To(specs.BeNil())
				return worker.Client{URL: srv.URL, Token: token}
			}
			slow, other := client("j-1"), client("j-2")

			first := make(chan error, 1)
			go func() {
				_, err := slow.RunGate(context.Background(), "j-1")
				first <- err
			}()
			l.waitRuns(ctx, 1)
			gitBefore, countsBefore := l.git.Load(), lines(l.counts)

			_, err := other.RunGate(context.Background(), "j-2")
			refused, ok := errors.AsType[*worker.RefusedError](err)
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(refused.Status).ToEqual(http.StatusTooManyRequests)
			ctx.Expect(refused.Message).ToEqual("worker: run gate: too many running: 1 run_gate calls are running, as many as may run at once; nothing ran, so call again once one ends")
			ctx.Expect(lines(l.calls)).ToEqual(1)
			ctx.Expect(lines(l.counts)).ToEqual(countsBefore)
			ctx.Expect(l.git.Load()).ToEqual(gitBefore)

			l.releaseRuns(ctx)
			ctx.Expect(<-first).To(specs.BeNil())

			third, err := other.RunGate(context.Background(), "j-2")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(third.Passed).To(specs.BeTrue())
			ctx.Expect(lines(l.calls)).ToEqual(2)
		})
	})
}
