package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/portrange"
	"github.com/pablogore/software-factory/internal/slices/build"
	"github.com/pablogore/software-factory/internal/wiring"
)

// demoJobs wait in build for a builder's result.
var demoJobs = []domain.JobID{"demo-1", "demo-2"}

// demoBuilder is the builder the demo issues tokens for, with the tools
// roles/builder.yaml lists, so it may run the job's checks and its dev
// server.
var demoBuilder = domain.Role{Name: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress", "run_gate", "dev_server"}}

// demoReviewer is a reviewer with no tools list: it may report progress
// and hand in its result, but not run the job's checks.
var demoReviewer = domain.Role{Name: "reviewer-code", Kind: domain.KindReview}

// demoSecurity is a security reviewer that lists dev_server, which is
// refused to it all the same: only the builder and the UX and UI reviewers
// run the app.
var demoSecurity = domain.Role{Name: "reviewer-security", Kind: domain.KindReview, Tools: []string{"submit_result", "progress", "dev_server"}}

type demoPipelines struct{}

// Pipeline is a build stage followed by a review, so a builder's result
// moves the job on.
func (demoPipelines) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if name == demoAskPipeline {
		return demoAsk(), nil
	}
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

// workerDemo serves the worker API, its HTTP routes and its MCP server, over
// an in-memory store with demoJobs, until ctx ends. It is not a factoryd
// server: it prints a builder's and a reviewer's token per job and, after
// every request, each job's stage, round and saved events, so a person can
// try `factory worker submit-result`, `factory worker run-gate` and an MCP
// client's tools against it. Every job's worktree is -dir, whose HEAD is
// recorded as its base, and its checks are -checks, as the gate runs them,
// within -gate-timeout and keeping -gate-max-output bytes of output. The
// builder's dev_server runs the command -dir's .factory/devserver.yaml sets
// at HEAD on a port of the job's range, from -port-base in ranges of
// -port-step ports. A server is stopped once its session ends: the role
// hands in its result or asks, its attempt fails or the job leaves the
// stage. Every server left is stopped when the demo ends, and the ones a
// crashed demo left, recorded in -dev-state, when the next one starts.
// POST newRoundPath, with no token, sends a job back to build in a new round,
// so a person can see its old token refused. Job demoAskJob waits in spec,
// with a spec-writer's token that may ask a person a question and a
// builder's that may not; POST answerPath, with no token and the answer as
// its body, answers it as a person would and prints the spec-writer's new
// token and what its agent is prompted with.
func workerDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("worker-demo", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "address to listen on")
	dir := fs.String("dir", ".", "the git repo every job's checks run in, as its worktree; its HEAD is the base the diff is judged against")
	command := fs.String("checks", strings.Join(build.DefaultCommand, " "), "the checks command, split on spaces and run without a shell")
	timeout := fs.Duration("gate-timeout", wiring.DefaultRunGateTimeout, "how long a whole run-gate call may take, checks, test counts and git steps included, before it is stopped with a timed out error")
	maxOutput := fs.Int("gate-max-output", wiring.DefaultRunGateMaxOutput, "the most bytes of the checks' output a run-gate keeps and returns")
	portBase := fs.Int("port-base", portrange.DefaultBase, "the first port of the jobs' dev server ranges")
	portStep := fs.Int("port-step", portrange.DefaultStep, "how many ports each job's range has")
	defaultState, err := wiring.DefaultDevServerState()
	if err != nil {
		return err
	}
	state := fs.String("dev-state", defaultState, "where running dev servers are recorded, so the next run kills what a crash left")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := loopback(*addr); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	head, err := revParse(ctx, abs, "HEAD")
	if err != nil {
		return err
	}
	store, err := memstore.New()
	if err != nil {
		return err
	}
	ranges, err := portrange.New(*portBase, *portStep)
	if err != nil {
		return err
	}
	tokens := jobtoken.New()
	issued := map[domain.JobID]string{}
	reviewers := map[domain.JobID]string{}
	security := map[domain.JobID]string{}
	for _, id := range demoJobs {
		job := domain.Job{ID: id, Title: "Demo job", Pipeline: "demo", Stage: "build", Round: 1}
		if err := store.Create(ctx, job); err != nil {
			return err
		}
		if err := store.RecordBase(ctx, id, head); err != nil {
			return err
		}
		if issued[id], err = tokens.Issue(jobtoken.For(job, nil, demoBuilder)); err != nil {
			return err
		}
		if reviewers[id], err = tokens.Issue(jobtoken.For(job, nil, demoReviewer)); err != nil {
			return err
		}
		if security[id], err = tokens.Issue(jobtoken.For(job, nil, demoSecurity)); err != nil {
			return err
		}
	}
	askWriter, askBuilder, err := createAskJob(ctx, store, tokens)
	if err != nil {
		return err
	}
	if *timeout <= 0 || *maxOutput <= 0 {
		return fmt.Errorf("%w: -gate-timeout and -gate-max-output must be positive", domain.ErrInvalidValue)
	}
	homes, err := childenv.DefaultHomes()
	if err != nil {
		return err
	}
	locks := gatelock.New()
	gate := wiring.RunGate(store, demoPipelines{}, dirWorkspace{abs}, store, wiring.SystemClock{}, locks, homes, strings.Fields(*command), nil, *timeout, *maxOutput)
	servers, err := wiring.StartDevServers(ctx, *state)
	if err != nil {
		return err
	}
	defer func() {
		for _, id := range demoJobs {
			_ = servers.StopJob(context.Background(), id)
		}
	}()
	dev := &wiring.DevServers{Servers: servers, Worktrees: dirWorkspace{abs}, Ports: ranges, Bases: store, Homes: homes}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *addr)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	_, _ = fmt.Fprintf(out, "worker demo on %s; jobs %s wait in build for a builder's result\n", url, joinIDs(demoJobs))
	_, _ = fmt.Fprintf(out, "run-gate runs `%s` in %s, judging the diff against %s, for at most %s, keeping at most %d bytes of output\n", strings.Join(gate.Command, " "), abs, head, *timeout, *maxOutput)
	_, _ = fmt.Fprintf(out, "dev-server runs the command %s sets at %s, on ports from %d, %d per job\n", wiring.DevServerFile, head, *portBase, *portStep)
	_, _ = fmt.Fprintf(out, "\nexport FACTORY_URL=%s\n", url)
	for _, id := range demoJobs {
		_, _ = fmt.Fprintf(out, "# job %s, builder\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, id, issued[id])
		_, _ = fmt.Fprintf(out, "# MCP: %s, header \"Authorization: Bearer $FACTORY_JOB_TOKEN\", tools %s, %s, %s and %s\n", mcpURL(url, id), mcpworker.ToolSubmitResult, mcpworker.ToolProgress, mcpworker.ToolRunGate, mcpworker.ToolDevServer)
		_, _ = fmt.Fprintf(out, "# job %s, reviewer: may not run the checks\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, id, reviewers[id])
		_, _ = fmt.Fprintf(out, "# job %s, reviewer-security: lists dev_server, may not run it\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, id, security[id])
	}
	_, _ = fmt.Fprintf(out, "# job %s waits in spec; spec-writer: may ask a person with `factory worker ask-human` or MCP tool %s\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n",
		demoAskJob, mcpworker.ToolAskHuman, demoAskJob, askWriter)
	_, _ = fmt.Fprintf(out, "# job %s, builder: may not ask a person\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", demoAskJob, demoAskJob, askBuilder)
	_, _ = fmt.Fprintf(out, "# answer its question as a person: curl -d 'the answer' %s%s\n", url, strings.Replace(answerPath, "{job}", string(demoAskJob), 1))
	_, _ = fmt.Fprintln(out)

	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.Handle("/", wiring.WorkerAPI(tokens, store, demoPipelines{}, gate, locks, wiring.SystemClock{}, dev, nil))
	events := bus.New(store, wiring.SystemClock{})
	if err := dev.Subscribe(events); err != nil {
		return err
	}
	if err := events.Start(ctx); err != nil {
		return err
	}
	mux.HandleFunc("POST "+newRoundPath, func(w http.ResponseWriter, r *http.Request) {
		id := domain.JobID(r.PathValue("job"))
		token, err := newRound(r.Context(), store, tokens, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		mu.Lock()
		_, _ = fmt.Fprintf(out, "# job %s, new build round; its old token is from an earlier round\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, id, token)
		_, _ = fmt.Fprintf(out, "# MCP: %s, header \"Authorization: Bearer $FACTORY_JOB_TOKEN\", tools %s, %s, %s and %s\n", mcpURL(url, id), mcpworker.ToolSubmitResult, mcpworker.ToolProgress, mcpworker.ToolRunGate, mcpworker.ToolDevServer)
		mu.Unlock()
		_, _ = fmt.Fprintf(w, "job %s is in a new build round\n", id)
	})
	mux.HandleFunc("POST "+answerPath, func(w http.ResponseWriter, r *http.Request) {
		id := domain.JobID(r.PathValue("job"))
		text, err := io.ReadAll(io.LimitReader(r.Body, domain.MaxProgressText))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		token, brief, err := answer(r.Context(), store, tokens, id, string(text))
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		mu.Lock()
		_, _ = fmt.Fprintf(out, "# job %s, answered; its stage runs again, and its spec-writer is prompted with:\n%s", id, brief)
		_, _ = fmt.Fprintf(out, "export FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, token)
		mu.Unlock()
		_, _ = fmt.Fprintf(w, "job %s is answered\n", id)
	})
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		dispatched := events.Dispatch(ctx)
		mu.Lock()
		defer mu.Unlock()
		if dispatched != nil {
			_, _ = fmt.Fprintf(out, "dispatching events: %v\n", dispatched)
		}
		_, _ = fmt.Fprintf(out, "%s %s\n", r.Method, r.URL.Path)
		for _, id := range append(slices.Clone(demoJobs), demoAskJob) {
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
	return tokens.Issue(jobtoken.For(job, hist, demoBuilder))
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
