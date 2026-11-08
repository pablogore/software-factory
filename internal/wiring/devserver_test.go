//go:build unix

package wiring_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/portrange"
	"github.com/pablogore/software-factory/internal/wiring"
	"github.com/pablogore/software-factory/pkg/worker"
)

// serveScript is a dev server that starts a child, as `npm run dev` does,
// and writes its group's id, the leader's pid, to the worktree.
const serveScript = "echo $$ > leader.pid.tmp; mv leader.pid.tmp leader.pid; sleep 300 & wait\n"

// devRepo is a git repo whose base commit configures serveScript as its
// dev server, and that commit.
func devRepo(ctx *specs.Context) (string, string) {
	dir := ctx.Testing().TempDir()
	ctx.Expect(os.MkdirAll(filepath.Join(dir, ".factory"), 0o755)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(filepath.Join(dir, "serve.sh"), []byte(serveScript), 0o600)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(filepath.Join(dir, wiring.DevServerFile), []byte("command: sh serve.sh\n"), 0o600)).To(specs.BeNil())
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "base"}} {
		ctx.Expect(exec.Command("git", append([]string{"-C", dir}, args...)...).Run()).To(specs.BeNil())
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	ctx.Expect(err).To(specs.BeNil())
	return dir, strings.TrimSpace(string(out))
}

// devWorld is the worker API with dev servers over a job, their stops
// subscribed to a started bus.
type devWorld struct {
	store  *memstore.Store
	events *bus.Bus
	dir    string
	tokens *jobtoken.Tokens
	url    string
}

func newDevWorld(ctx *specs.Context, job domain.Job) devWorld {
	bg := context.Background()
	dir, base := devRepo(ctx)
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(bg, job)).To(specs.BeNil())
	ctx.Expect(store.RecordBase(bg, job.ID, base)).To(specs.BeNil())
	servers, err := wiring.StartDevServers(bg, ctx.Testing().TempDir())
	ctx.Expect(err).To(specs.BeNil())
	ctx.Cleanup(func() { _ = servers.StopJob(bg, job.ID) })
	ranges, err := portrange.New(portrange.DefaultBase, portrange.DefaultStep)
	ctx.Expect(err).To(specs.BeNil())
	dev := &wiring.DevServers{Servers: servers, Worktrees: oneDir(dir), Ports: ranges, Bases: store}
	tokens := jobtoken.New()
	srv := httptest.NewServer(wiring.WorkerAPI(tokens, store, onePipeline{}, nil, gatelock.New(), fixedClock{}, dev))
	ctx.Cleanup(srv.Close)
	events := bus.New(store, fixedClock{})
	ctx.Expect(dev.Subscribe(events)).To(specs.BeNil())
	ctx.Expect(events.Start(bg)).To(specs.BeNil())
	return devWorld{store: store, events: events, dir: dir, tokens: tokens, url: srv.URL}
}

// start starts role's dev server on job and returns its group's id.
func (w devWorld) start(ctx *specs.Context, job domain.Job, role domain.Role) int {
	token, err := w.tokens.Issue(jobtoken.For(job, nil, role))
	ctx.Expect(err).To(specs.BeNil())
	_ = os.Remove(filepath.Join(w.dir, "leader.pid"))
	_, err = worker.Client{URL: w.url, Token: token}.DevServer(context.Background(), string(job.ID))
	ctx.Expect(err).To(specs.BeNil())
	var pgid int
	ctx.Expect(within(func() bool {
		b, err := os.ReadFile(filepath.Join(w.dir, "leader.pid"))
		pgid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && pgid > 0
	})).To(specs.BeTrue())
	return pgid
}

// apply decides ev on job and dispatches the events it saved.
func (w devWorld) apply(ctx *specs.Context, id domain.JobID, ev domain.Event) {
	_, v, err := w.store.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	p, _ := onePipeline{}.Pipeline(context.Background(), "default")
	_, _, err = w.store.Apply(context.Background(), id, v, p, ev)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(w.events.Dispatch(context.Background())).To(specs.BeNil())
}

func within(ok func() bool) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return true
		}
	}
	return ok()
}

func groupGone(pgid int) bool {
	return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

var at = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

var devBuilder = domain.Role{Name: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "dev_server"}}

type endRow struct {
	name string
	job  domain.Job
	ev   domain.Event
}

func TestDevServerStops(t *testing.T) {
	specs.Describe(t, "a session's dev server", func(s *specs.Spec) {
		inBuild := domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "build", Round: 1}
		slice := inBuild
		slice.Initiative = "i-1"
		merging := inBuild
		merging.Stage = domain.StageMerge
		ends := []endRow{
			{"after its attempt fails", inBuild, domain.AttemptFailed{Job: "j-1", Role: "builder", Reason: "no result", At: at}},
			{"once the job leaves the stage", inBuild, domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at}},
			{"once the job is closed, merged", merging, domain.MergeCompleted{Job: "j-1", At: at}},
			{"once the job is stopped", inBuild, domain.StopRequested{Job: "j-1", By: "pablo", At: at}},
			{"once the slice is cancelled, dropped by a person's direction", slice, domain.MessageReceived{
				Job: "j-1", Provider: "github", Thread: "1", Author: "pablo", AuthorKind: domain.AuthorPerson, Allowed: true, Direction: true, Text: "drop it", At: at,
			}},
		}
		specs.Table(s, ends, func(r endRow) string { return "is killed with its whole group " + r.name }, func(ctx *specs.Context, r endRow) {
			w := newDevWorld(ctx, r.job)
			pgid := w.start(ctx, r.job, devBuilder)
			ctx.Expect(groupGone(pgid)).To(specs.BeFalse())

			w.apply(ctx, r.job.ID, r.ev)

			ctx.Expect(within(func() bool { return groupGone(pgid) })).To(specs.BeTrue())
		})

		s.It("keeps running while its session goes on: another role's attempt failed", func(ctx *specs.Context) {
			w := newDevWorld(ctx, inBuild)
			pgid := w.start(ctx, inBuild, devBuilder)

			w.apply(ctx, "j-1", domain.AttemptFailed{Job: "j-1", Role: "reviewer-ux", Reason: "no result", At: at})

			ctx.Expect(groupGone(pgid)).To(specs.BeFalse())
		})
	})
}
