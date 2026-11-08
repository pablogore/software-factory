//go:build unix

package wiring_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/wiring"
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

// checkWith runs command as run_gate does, on a job in build whose worktree
// is a fresh git repo, within timeout and maxOutput.
func checkWith(ctx *specs.Context, command []string, timeout time.Duration, maxOutput int) domain.ChecksRun {
	dir, base := gitRepo(ctx)
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
	ctx.Expect(store.RecordBase(context.Background(), "j-1", base)).To(specs.BeNil())
	g := wiring.RunGate(store, onePipeline{}, oneDir(dir), store, fixedClock{}, command, nil, timeout, maxOutput)
	run, err := g.Check(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return run
}

func TestRunGateLimits(t *testing.T) {
	specs.Describe(t, "the gate run_gate runs", func(s *specs.Spec) {
		s.It("kills checks past its timeout and fails the run, saying so", func(ctx *specs.Context) {
			run := checkWith(ctx, []string{"sh", "-c", "sleep 30"}, 300*time.Millisecond, wiring.DefaultRunGateMaxOutput)
			ctx.Expect(run.Passed()).To(specs.BeFalse())
			ctx.Expect(run.Notes).To(specs.Contain("timed out: the checks ran over 300ms and were killed with their process group"))
		})

		s.It("keeps at most its output limit, saying it cut the rest", func(ctx *specs.Context) {
			run := checkWith(ctx, []string{"sh", "-c", "head -c 5000 /dev/zero"}, time.Minute, 100)
			ctx.Expect(len(run.Output)).ToEqual(100)
			ctx.Expect(run.Notes).To(specs.Contain("output truncated: kept the first 100 bytes, dropped the other 4900"))
		})
	})
}
