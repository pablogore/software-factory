package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/wiring"
)

// demoJobs wait in build for a builder's result.
var demoJobs = []domain.JobID{"demo-1", "demo-2"}

type demoPipelines struct{}

// Pipeline is a build stage followed by a review, so a builder's result
// moves the job on.
func (demoPipelines) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if name != "demo" {
		return domain.Pipeline{}, fmt.Errorf("no pipeline %q", name)
	}
	return domain.Pipeline{Name: "demo", Stages: []domain.Stage{
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
	}}, nil
}

// newRoundPath is the demo's own route that takes a job to a new build
// round, as a failed gate does, and prints a builder's token for it.
const newRoundPath = "/demo/jobs/{job}/new-round"

// workerDemo serves the worker API, its HTTP route and its MCP server, over
// an in-memory store with demoJobs, until ctx ends. It is not a factoryd
// server: it prints a builder's token per job and, after every request, each
// job's stage, round and saved events, so a person can try `factory worker submit-result` and an MCP
// client's submit_result against it.
// POST newRoundPath, with no token, sends a job back to build in a new round,
// so a person can see its old token refused.
func workerDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("worker-demo", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := loopback(*addr); err != nil {
		return err
	}
	store, err := memstore.New()
	if err != nil {
		return err
	}
	tokens := jobtoken.New()
	issued := map[domain.JobID]string{}
	for _, id := range demoJobs {
		job := domain.Job{ID: id, Title: "Demo job", Pipeline: "demo", Stage: "build", Round: 1}
		if err := store.Create(ctx, job); err != nil {
			return err
		}
		if issued[id], err = tokens.Issue(jobtoken.For(job, nil, domain.Role{Name: "builder", Kind: domain.KindBuild})); err != nil {
			return err
		}
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *addr)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	_, _ = fmt.Fprintf(out, "worker demo on %s; jobs %s wait in build for a builder's result\n", url, joinIDs(demoJobs))
	_, _ = fmt.Fprintf(out, "\nexport FACTORY_URL=%s\n", url)
	for _, id := range demoJobs {
		_, _ = fmt.Fprintf(out, "# job %s\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, id, issued[id])
		_, _ = fmt.Fprintf(out, "# MCP: %s, header \"Authorization: Bearer $FACTORY_JOB_TOKEN\", tools %s and %s\n", mcpURL(url, id), mcpworker.ToolSubmitResult, mcpworker.ToolProgress)
	}
	_, _ = fmt.Fprintln(out)

	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.Handle("/", wiring.WorkerAPI(tokens, store, demoPipelines{}, wiring.SystemClock{}))
	mux.HandleFunc("POST "+newRoundPath, func(w http.ResponseWriter, r *http.Request) {
		id := domain.JobID(r.PathValue("job"))
		token, err := newRound(r.Context(), store, tokens, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		mu.Lock()
		_, _ = fmt.Fprintf(out, "# job %s, new build round; its old token is from an earlier round\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, id, token)
		_, _ = fmt.Fprintf(out, "# MCP: %s, header \"Authorization: Bearer $FACTORY_JOB_TOKEN\", tools %s and %s\n", mcpURL(url, id), mcpworker.ToolSubmitResult, mcpworker.ToolProgress)
		mu.Unlock()
		_, _ = fmt.Fprintf(w, "job %s is in a new build round\n", id)
	})
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(out, "%s %s\n", r.Method, r.URL.Path)
		for _, id := range demoJobs {
			job, _, _ := store.Load(ctx, id)
			hist, _ := store.History(ctx, id)
			names := make([]string, len(hist))
			for i, h := range hist {
				names[i] = eventLabel(h.Event)
			}
			_, _ = fmt.Fprintf(out, "  %s: stage %s, round %d, events [%s]\n", id, job.Stage, job.Round, strings.Join(names, ", "))
		}
	})}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newRound takes job id to build in a new round, as its build being ready
// and the gate failing would, and returns a builder's token for that round.
func newRound(ctx context.Context, store *memstore.Store, tokens *jobtoken.Tokens, id domain.JobID) (string, error) {
	p, err := demoPipelines{}.Pipeline(ctx, "demo")
	if err != nil {
		return "", err
	}
	job, v, err := store.Load(ctx, id)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if job.Stage == "build" {
		ready := domain.ResultSubmitted{Job: id, Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: now}
		if _, v, err = store.Apply(ctx, id, v, p, ready); err != nil {
			return "", err
		}
	}
	if _, _, err := store.Apply(ctx, id, v, p, domain.GateFailed{Job: id, Run: domain.ChecksRun{Command: "demo checks", ExitCode: 1}, At: now}); err != nil {
		return "", err
	}
	if job, _, err = store.Load(ctx, id); err != nil {
		return "", err
	}
	hist, err := store.History(ctx, id)
	if err != nil {
		return "", err
	}
	return tokens.Issue(jobtoken.For(job, hist, domain.Role{Name: "builder", Kind: domain.KindBuild}))
}

// errNotLoopback refuses an address other machines could reach: the demo's
// newRoundPath takes no token.
var errNotLoopback = errors.New("worker demo listens on loopback only, since " + newRoundPath + " takes no token; use 127.0.0.1, ::1 or localhost")

// loopback refuses addr unless its host is localhost or a loopback IP. An
// empty host or 0.0.0.0 listens on every interface, so neither is one.
func loopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("--addr %q: %w", addr, errNotLoopback)
	}
	return nil
}

// mcpURL is the MCP endpoint of job id on the demo at base.
func mcpURL(base string, id domain.JobID) string {
	return base + strings.Replace(mcpworker.MCPPath, "{job}", string(id), 1)
}

func joinIDs(ids []domain.JobID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = string(id)
	}
	return strings.Join(s, " and ")
}
