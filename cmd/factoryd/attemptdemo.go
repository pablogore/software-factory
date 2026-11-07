package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/wiring"
	"github.com/pablogore/software-factory/pkg/worker"
)

// attemptDemoAgents are the test agents of attempt-demo: each turn submits
// its JSON through the worker API, as `factory worker submit-result` does,
// or nothing when it is empty.
var attemptDemoAgents = []struct {
	job   domain.JobID
	about string
	turns []string
}{
	{"demo-1", "an agent that hands in an invalid JSON, then a valid one", []string{`{"status":"DONE"}`, `{"status":"READY","summary":"built"}`}},
	{"demo-2", "an agent that never hands in a result", nil},
}

// attemptDemo runs one attempt of each of attemptDemoAgents at its own job
// waiting in build, over the worker API on 127.0.0.1, and prints the prompts
// each agent got, what it submitted and what its job saved.
func attemptDemo(ctx context.Context, out io.Writer) error {
	store, err := memstore.New()
	if err != nil {
		return err
	}
	tokens := jobtoken.New()
	api, attempts := wiring.Worker(tokens, store, demoPipelines{}, wiring.SystemClock{})
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: api}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	for _, a := range attemptDemoAgents {
		job := domain.Job{ID: a.job, Title: "Demo job", Pipeline: "demo", Stage: "build", Round: 1}
		if err := store.Create(ctx, job); err != nil {
			return err
		}
		token, err := tokens.Issue(jobtoken.For(job, nil, "builder", domain.KindBuild))
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "== job %s: %s\n", a.job, a.about)
		agent := &demoAgent{out: out, job: a.job, turns: a.turns, client: worker.Client{URL: "http://" + ln.Addr().String(), Token: token}}
		saved, err := attempts.Run(ctx, agent, a.job, "builder", token, "Build the demo job and hand in your result with submit_result.")
		if err != nil {
			return err
		}
		if err := printJob(ctx, out, store, a.job, saved); err != nil {
			return err
		}
	}
	return nil
}

// demoAgent is a test agent session: it prints every prompt it gets and
// answers turn i by submitting turns[i].
type demoAgent struct {
	out    io.Writer
	job    domain.JobID
	turns  []string
	client worker.Client
	n      int
}

func (a *demoAgent) Prompt(ctx context.Context, msg string) (<-chan domain.AgentProgressed, error) {
	_, _ = fmt.Fprintf(a.out, "-> prompt %d to the agent:\n%s\n", a.n+1, indent(msg))
	if a.n < len(a.turns) && a.turns[a.n] != "" {
		acc, err := a.client.SubmitResult(ctx, string(a.job), []byte(a.turns[a.n]))
		var refused *worker.RefusedError
		switch {
		case errors.As(err, &refused):
			_, _ = fmt.Fprintf(a.out, "<- the agent submits %s: %v\n", a.turns[a.n], err)
		case err != nil:
			return nil, err
		default:
			_, _ = fmt.Fprintf(a.out, "<- the agent submits %s: recorded, %s\n", a.turns[a.n], strings.Join(acc.Events, ", "))
		}
	} else {
		_, _ = fmt.Fprintln(a.out, "<- the agent ends its turn without submitting anything")
	}
	a.n++
	ch := make(chan domain.AgentProgressed, 1)
	ch <- domain.AgentProgressed{Job: a.job, Role: "builder", Kind: domain.ProgressEnded, StopReason: "end_turn"}
	close(ch)
	return ch, nil
}

func printJob(ctx context.Context, out io.Writer, store *memstore.Store, id domain.JobID, saved bool) error {
	job, _, err := store.Load(ctx, id)
	if err != nil {
		return err
	}
	hist, err := store.History(ctx, id)
	if err != nil {
		return err
	}
	names := make([]string, len(hist))
	for i, h := range hist {
		names[i] = h.Event.EventName()
		if f, ok := h.Event.(domain.AttemptFailed); ok {
			names[i] += fmt.Sprintf("(role %s, reason %q)", f.Role, f.Reason)
		}
	}
	_, _ = fmt.Fprintf(out, "result recorded: %v; job %s: stage %s, events [%s]\n\n", saved, id, job.Stage, strings.Join(names, ", "))
	return nil
}

func indent(s string) string {
	return "   | " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n   | ")
}
