package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/wiring"
)

var at = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

type stopped struct{}

func (stopped) Now() time.Time { return at }

type onePipeline struct{}

func (onePipeline) Pipeline(context.Context, string) (domain.Pipeline, error) {
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
	}}, nil
}

// factoryd is a worker API over an in-memory store with jobs j-1 and j-2
// in build, and a builder's token for each.
type factoryd struct {
	url    string
	store  *memstore.Store
	tokens map[domain.JobID]string
}

func start(ctx *specs.Context) factoryd {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	tokens := jobtoken.New()
	f := factoryd{store: store, tokens: map[domain.JobID]string{}}
	for _, id := range []domain.JobID{"j-1", "j-2"} {
		ctx.Expect(store.Create(context.Background(), domain.Job{ID: id, Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
		f.tokens[id], err = tokens.Issue(jobtoken.Grant{Job: id, Role: "builder", Kind: domain.KindBuild, Round: 1})
		ctx.Expect(err).To(specs.BeNil())
	}
	srv := httptest.NewServer(wiring.WorkerAPI(tokens, store, onePipeline{}, stopped{}))
	ctx.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

// events lists the names of the events job id saved.
func (f factoryd) events(ctx *specs.Context, id domain.JobID) []string {
	hist, err := f.store.History(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	names := []string{}
	for _, h := range hist {
		names = append(names, h.Event.EventName())
	}
	return names
}

type run struct {
	code           int
	stdout, stderr string
}

func cli(env map[string]string, stdin string, args ...string) run {
	var out, errOut bytes.Buffer
	code := runWorker(context.Background(), args, func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errOut)
	return run{code, out.String(), errOut.String()}
}

func (f factoryd) env(job domain.JobID, token string) map[string]string {
	return map[string]string{"FACTORY_URL": f.url, "FACTORY_JOB_ID": string(job), "FACTORY_JOB_TOKEN": token}
}

func TestWorkerSubmitResult(t *testing.T) {
	specs.Describe(t, "factory worker submit-result", func(s *specs.Spec) {
		s.It("records a valid result of the token's kind as the job's result", func(ctx *specs.Context) {
			f := start(ctx)
			r := cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY","summary":"done"}`, "submit-result")
			ctx.Expect(r.code).ToEqual(0)
			ctx.Expect(r.stdout).ToEqual("result recorded for job j-1: StageEntered\n")
			ctx.Expect(f.events(ctx, "j-1")).ToEqual([]string{"StageEntered"})
			job, _, err := f.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual(domain.StageGate)
			ctx.Expect(f.events(ctx, "j-2")).To(specs.BeEmpty())
		})

		s.It("reads the result from a file and takes the URL and job from flags", func(ctx *specs.Context) {
			f := start(ctx)
			file := filepath.Join(ctx.Testing().TempDir(), "result.json")
			ctx.Expect(os.WriteFile(file, []byte(`{"status":"READY"}`), 0o600)).To(specs.BeNil())
			env := map[string]string{"FACTORY_JOB_TOKEN": f.tokens["j-2"]}
			r := cli(env, "", "submit-result", "--url", f.url, "--job", "j-2", file)
			ctx.Expect(r.code).ToEqual(0)
			ctx.Expect(f.events(ctx, "j-2")).ToEqual([]string{"StageEntered"})
		})

		s.It("fails on a result invalid for the kind, saying what is wrong, and records nothing", func(ctx *specs.Context) {
			f := start(ctx)
			r := cli(f.env("j-1", f.tokens["j-1"]), `{"status":"DONE"}`, "submit-result")
			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stderr).To(specs.Contain("422"))
			ctx.Expect(r.stderr).To(specs.Contain(`build result: invalid value: status "DONE"`))
			ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
		})

		s.When("the token is not this job's", func(s *specs.Spec) {
			s.It("refuses another job's token and records nothing on either job", func(ctx *specs.Context) {
				f := start(ctx)
				r := cli(f.env("j-1", f.tokens["j-2"]), `{"status":"READY"}`, "submit-result")
				ctx.Expect(r.code).ToEqual(1)
				ctx.Expect(r.stderr).To(specs.Contain("403"))
				ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
				ctx.Expect(f.events(ctx, "j-2")).To(specs.BeEmpty())
			})

			s.It("refuses an unknown token and records nothing", func(ctx *specs.Context) {
				f := start(ctx)
				r := cli(f.env("j-1", "0123abcd"), `{"status":"READY"}`, "submit-result")
				ctx.Expect(r.code).ToEqual(1)
				ctx.Expect(r.stderr).To(specs.Contain("401"))
				ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
			})

			s.It("does not call factoryd without a token", func(ctx *specs.Context) {
				f := start(ctx)
				r := cli(f.env("j-1", ""), `{"status":"READY"}`, "submit-result")
				ctx.Expect(r.code).ToEqual(2)
				ctx.Expect(r.stderr).To(specs.Contain("FACTORY_JOB_TOKEN"))
				ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
			})
		})

		s.It("refuses a second result with the same token and records nothing more", func(ctx *specs.Context) {
			f := start(ctx)
			ctx.Expect(cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY"}`, "submit-result").code).ToEqual(0)
			r := cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY"}`, "submit-result")
			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stderr).To(specs.Contain("401"))
			ctx.Expect(f.events(ctx, "j-1")).ToEqual([]string{"StageEntered"})
		})

		s.It("refuses a token of an earlier round, saying so, though the job is back in build", func(ctx *specs.Context) {
			f := start(ctx)
			for _, ev := range []domain.Event{
				domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at},
				domain.GateFailed{Job: "j-1", At: at},
			} {
				_, v, err := f.store.Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				p, _ := onePipeline{}.Pipeline(context.Background(), "default")
				_, _, err = f.store.Apply(context.Background(), "j-1", v, p, ev)
				ctx.Expect(err).To(specs.BeNil())
			}
			before := f.events(ctx, "j-1")
			r := cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY"}`, "submit-result")
			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stderr).To(specs.Contain("409"))
			ctx.Expect(r.stderr).To(specs.Contain("job token from an earlier round: issued in round 1"))
			ctx.Expect(f.events(ctx, "j-1")).ToEqual(before)
		})

		s.It("knows no other worker command", func(ctx *specs.Context) {
			f := start(ctx)
			r := cli(f.env("j-1", f.tokens["j-1"]), "", "approve")
			ctx.Expect(r.code).ToEqual(2)
			ctx.Expect(r.stderr).To(specs.Contain("usage: factory worker submit-result"))
		})
	})
}
