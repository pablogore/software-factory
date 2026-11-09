//go:build unix

package wiring_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/slices/build"
	"github.com/pablogore/software-factory/internal/wiring"
	"github.com/pablogore/software-factory/pkg/worker"
)

// sharedGate is the worker API, with run_gate running a long check that
// leaves a child in the background, and the job's real gate, over one
// store and one set of locks, on j-1 in build with a git repo as its
// worktree. pidFile, outside the worktree, has the child's pid once the
// long check started.
type sharedGate struct {
	store   *memstore.Store
	gate    *build.Gate
	client  worker.Client
	pidFile string
}

func newSharedGate(ctx *specs.Context) sharedGate {
	dir, base := gitRepo(ctx)
	pidFile := filepath.Join(ctx.Testing().TempDir(), "child.pid")
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
	ctx.Expect(store.RecordBase(context.Background(), "j-1", base)).To(specs.BeNil())
	locks := gatelock.New()
	homes := childenv.Homes{Root: ctx.Testing().TempDir()}
	long := []string{"sh", "-c", "sleep 60 >/dev/null 2>&1 & echo $! > " + pidFile + "; wait"}
	runGate := wiring.RunGate(store, onePipeline{}, oneDir(dir), store, fixedClock{}, locks, homes, long, nil, time.Minute, wiring.DefaultRunGateMaxOutput)
	// The real gate passes only when no process of the run_gate run is left.
	noneLeft := []string{"sh", "-c", "! kill -0 $(cat " + pidFile + ") 2>/dev/null"}
	gate := wiring.Gate(store, onePipeline{}, oneDir(dir), store, fixedClock{}, locks, homes, noneLeft, nil)
	tokens := jobtoken.New()
	token, err := tokens.Issue(jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress", "run_gate"}, Round: 1})
	ctx.Expect(err).To(specs.BeNil())
	srv := httptest.NewServer(wiring.WorkerAPI(tokens, store, onePipeline{}, runGate, locks, fixedClock{}, nil))
	ctx.Cleanup(srv.Close)
	return sharedGate{store: store, gate: gate, client: worker.Client{URL: srv.URL, Token: token}, pidFile: pidFile}
}

// startLong runs run_gate in the background and returns its answer's
// channel and the pid of the child its check left, once it started.
func (g sharedGate) startLong(ctx *specs.Context) (<-chan error, int) {
	answer := make(chan error, 1)
	go func() {
		_, err := g.client.RunGate(context.Background(), "j-1")
		answer <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(g.pidFile)
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && convErr == nil {
			return answer, pid
		}
		ctx.Expect(time.Now().Before(deadline)).To(specs.BeTrue())
		time.Sleep(10 * time.Millisecond)
	}
}

func refusedWith(ctx *specs.Context, err error) *worker.RefusedError {
	refused, ok := errors.AsType[*worker.RefusedError](err)
	ctx.Expect(ok).To(specs.BeTrue())
	return refused
}

func TestRunGateAndTheGate(t *testing.T) {
	specs.Describe(t, "run_gate and the job's real gate", func(s *specs.Spec) {
		s.It("cancels a long run_gate on submit, so the real gate starts on a worktree with none of its processes", func(ctx *specs.Context) {
			g := newSharedGate(ctx)
			answer, child := g.startLong(ctx)

			accepted, err := g.client.SubmitResult(context.Background(), "j-1", []byte(`{"status":"READY"}`))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(accepted.Events).ToEqual([]string{"StageEntered"})
			ctx.Expect(syscall.Kill(child, 0)).To(specs.MatchError(syscall.ESRCH))
			refused := refusedWith(ctx, <-answer)
			ctx.Expect(refused.Status).ToEqual(409)
			ctx.Expect(refused.Message).To(specs.Contain("checks cancelled: the job's result was submitted"))

			evs, err := g.gate.Run(context.Background(), "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(names(evs)).ToEqual([]string{"ChecksRan", "StageEntered"})
			job, _, err := g.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual("review")
		})

		s.It("answers a second run_gate while the first runs with 409, running no other check", func(ctx *specs.Context) {
			g := newSharedGate(ctx)
			answer, _ := g.startLong(ctx)

			_, err := g.client.RunGate(context.Background(), "j-1")

			refused := refusedWith(ctx, err)
			ctx.Expect(refused.Status).ToEqual(409)
			ctx.Expect(refused.Message).To(specs.Contain(`already running: the checks of job "j-1" are running`))
			_, err = g.client.SubmitResult(context.Background(), "j-1", []byte(`{"status":"READY"}`))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(refusedWith(ctx, <-answer).Status).ToEqual(409)
		})
	})
}

func names(evs []domain.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.EventName()
	}
	return out
}
