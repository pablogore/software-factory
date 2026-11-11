//go:build unix

package wiring_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/wiring"
	"github.com/pablogore/software-factory/pkg/worker"
)

// oneDir hands every job dir as its worktree.
type oneDir string

func (d oneDir) Prepare(_ context.Context, job domain.JobID) (ports.Worktree, error) {
	return ports.Worktree{Job: job, Path: string(d)}, nil
}

func (oneDir) Clean(context.Context, domain.JobID) error { return nil }

// gitRepo is a new git repo with one empty commit, and that commit.
func gitRepo(ctx *specs.Context) (string, string) {
	dir := ctx.Testing().TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		ctx.Expect(exec.Command("git", append([]string{"-C", dir}, args...)...).Run()).To(specs.BeNil())
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	ctx.Expect(err).To(specs.BeNil())
	return dir, strings.TrimSpace(string(out))
}

// newRunGate is the gate run_gate runs, on a job j-1 in build whose
// worktree is a fresh git repo, running command and counting tests with
// count, within timeout and maxOutput.
func newRunGate(ctx *specs.Context, command, count []string, timeout time.Duration, maxOutput int) (*wiring.RunGateChecks, *memstore.Store) {
	dir, base := gitRepo(ctx)
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
	ctx.Expect(store.RecordBase(context.Background(), "j-1", base)).To(specs.BeNil())
	return wiring.RunGate(store, onePipeline{}, oneDir(dir), store, fixedClock{}, gatelock.New(), childenv.Homes{Root: ctx.Testing().TempDir()}, command, count, timeout, maxOutput), store
}

// checkWith runs command as run_gate does, within timeout and maxOutput.
func checkWith(ctx *specs.Context, command []string, timeout time.Duration, maxOutput int) domain.ChecksRun {
	g, _ := newRunGate(ctx, command, nil, timeout, maxOutput)
	run, err := g.Check(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return run
}

// hangingGit is git that hangs on every diff until its ctx ends, as a git
// step stuck on a lock or a filter would, and tells the deadline it ran
// with on deadlines.
type hangingGit struct {
	deadlines chan time.Time
}

func (h hangingGit) Changes(ctx context.Context, _ domain.JobID, _, _ string) ([]domain.FileChange, error) {
	if d, ok := ctx.Deadline(); ok && h.deadlines != nil {
		h.deadlines <- d
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (hangingGit) Lines(context.Context, domain.JobID, string, string) (int, error) { return 0, nil }

func (hangingGit) Flagged(context.Context, domain.JobID, string) ([]domain.FlaggedFile, error) {
	return nil, nil
}

func (hangingGit) AtBase(context.Context, domain.JobID, string, string, string) (string, error) {
	return "", nil
}

func (hangingGit) InWorktree(context.Context, domain.JobID, string, string) (string, error) {
	return "", nil
}

// timedCheck runs g's check of j-1 and returns its answer and how long it
// took.
func timedCheck(g *wiring.RunGateChecks) (domain.ChecksRun, time.Duration, error) {
	start := time.Now()
	run, err := g.Check(context.Background(), "j-1")
	return run, time.Since(start), err
}

func TestRunGateLimits(t *testing.T) {
	specs.Describe(t, "the gate run_gate runs", func(s *specs.Spec) {
		s.It("stops checks past its timeout and answers with a timed out error, not a failed run", func(ctx *specs.Context) {
			g, _ := newRunGate(ctx, []string{"sh", "-c", "sleep 30"}, nil, 300*time.Millisecond, wiring.DefaultRunGateMaxOutput)
			run, took, err := timedCheck(g)
			ctx.Expect(err).To(specs.MatchError(domain.ErrTimedOut))
			ctx.Expect(err.Error()).ToEqual("timed out: the call ran over 300ms and was stopped, checks, test counts and git steps included; it has no result, so call again")
			ctx.Expect(run).ToEqual(domain.ChecksRun{})
			ctx.Expect(took < 10*time.Second).To(specs.BeTrue())
		})

		s.It("counts the whole call against its timeout, checks and test counts together, not each command alone", func(ctx *specs.Context) {
			slow := []string{"sh", "-c", "sleep 0.6"}
			g, _ := newRunGate(ctx, slow, slow, time.Second, wiring.DefaultRunGateMaxOutput)
			_, took, err := timedCheck(g)
			ctx.Expect(err).To(specs.MatchError(domain.ErrTimedOut))
			ctx.Expect(took < 10*time.Second).To(specs.BeTrue())
		})

		s.It("stops a git step that hangs once its timeout passes", func(ctx *specs.Context) {
			g, _ := newRunGate(ctx, []string{"true"}, nil, 300*time.Millisecond, wiring.DefaultRunGateMaxOutput)
			g.Diffs = hangingGit{}
			_, took, err := timedCheck(g)
			ctx.Expect(err).To(specs.MatchError(domain.ErrTimedOut))
			ctx.Expect(took < 10*time.Second).To(specs.BeTrue())
		})

		s.It("gives the whole call 15 minutes when no timeout is set", func(ctx *specs.Context) {
			g, _ := newRunGate(ctx, []string{"true"}, nil, 0, wiring.DefaultRunGateMaxOutput)
			deadlines := make(chan time.Time, 1)
			g.Diffs = hangingGit{deadlines: deadlines}
			start := time.Now()
			call, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				_, err := g.Check(call, "j-1")
				done <- err
			}()
			deadline := <-deadlines
			cancel()
			ctx.Expect(<-done).To(specs.Not(specs.BeNil()))
			ctx.Expect(wiring.DefaultRunGateTimeout).ToEqual(15 * time.Minute)
			left := deadline.Sub(start)
			ctx.Expect(left > 14*time.Minute && left <= 15*time.Minute+time.Second).To(specs.BeTrue())
		})

		s.It("keeps at most its output limit, saying it cut the rest", func(ctx *specs.Context) {
			run := checkWith(ctx, []string{"sh", "-c", "head -c 5000 /dev/zero"}, time.Minute, 100)
			ctx.Expect(len(run.Output)).ToEqual(100)
			ctx.Expect(run.Notes).To(specs.Contain("output truncated: kept the first 100 bytes, dropped the other 4900"))
		})
	})
}

// TestDemoRunGateTimeout is the slice's demo: through the worker API, a
// run_gate call whose checks hang is cut at its short timeout with a timed
// out error, and the next call on the same job runs normally.
func TestDemoRunGateTimeout(t *testing.T) {
	specs.Describe(t, "a run_gate call past its timeout", func(s *specs.Spec) {
		s.It("is cut with a timed out error, and leaves the job free for the next call", func(ctx *specs.Context) {
			marker := filepath.Join(ctx.Testing().TempDir(), "hung-once")
			// The checks hang the first time only, as a stuck test would.
			once := []string{"sh", "-c", "if [ -f " + marker + " ]; then exit 0; fi; touch " + marker + "; sleep 60"}
			g, store := newRunGate(ctx, once, nil, 500*time.Millisecond, wiring.DefaultRunGateMaxOutput)
			tokens := jobtoken.New()
			token, err := tokens.Issue(jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress", "run_gate"}, Round: 1})
			ctx.Expect(err).To(specs.BeNil())
			srv := httptest.NewServer(wiring.WorkerAPI(tokens, store, onePipeline{}, g, g.Locks.(*gatelock.Locks), fixedClock{}, nil, nil))
			ctx.Cleanup(srv.Close)
			client := worker.Client{URL: srv.URL, Token: token}

			start := time.Now()
			_, err = client.RunGate(context.Background(), "j-1")
			took := time.Since(start)
			refused, ok := errors.AsType[*worker.RefusedError](err)
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(refused.Status).ToEqual(http.StatusGatewayTimeout)
			ctx.Expect(refused.Message).ToEqual("worker: run gate: timed out: the call ran over 500ms and was stopped, checks, test counts and git steps included; it has no result, so call again")
			ctx.Expect(took < 10*time.Second).To(specs.BeTrue())

			again, err := client.RunGate(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(again.Passed).To(specs.BeTrue())
		})
	})
}
