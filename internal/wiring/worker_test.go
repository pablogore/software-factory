package wiring_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/wiring"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC) }

type onePipeline struct{}

func (onePipeline) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if name != "default" {
		return domain.Pipeline{}, fmt.Errorf("no pipeline %q", name)
	}
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder", "only-submits", "no-tools"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
		{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
	}}, nil
}

// bearer sets the job token on every request, as an agent's MCP client
// does with the headers its session was given.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// workerWorld is factoryd's worker API over an in-memory store with job j-1
// in build, whose checks gate runs, served on a local test server.
type workerWorld struct {
	store  *memstore.Store
	tokens *jobtoken.Tokens
	gate   *gate
	url    string
}

// gate answers every check with a passing run and records the jobs it
// checked.
type gate struct {
	mu   sync.Mutex
	jobs []domain.JobID
}

func (g *gate) Check(_ context.Context, id domain.JobID) (domain.ChecksRun, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.jobs = append(g.jobs, id)
	return domain.ChecksRun{Command: "make check", Output: "ok\n"}, nil
}

func (g *gate) checked() []domain.JobID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]domain.JobID{}, g.jobs...)
}

func newWorkerWorld(ctx *specs.Context) workerWorld {
	return newWorkerWorldIn(ctx, "build")
}

// newWorkerWorldIn is newWorkerWorld with j-1 in stage.
func newWorkerWorldIn(ctx *specs.Context, stage string) workerWorld {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: stage, Round: 1})).To(specs.BeNil())
	tokens := jobtoken.New()
	g := &gate{}
	srv := httptest.NewServer(wiring.WorkerAPI(tokens, store, onePipeline{}, g, gatelock.New(), fixedClock{}))
	ctx.Cleanup(srv.Close)
	return workerWorld{store: store, tokens: tokens, gate: g, url: srv.URL}
}

// session issues a token for a session of role, loaded from dir, on j-1 and
// opens that session's MCP connection.
func (w workerWorld) session(ctx *specs.Context, dir, role string) *mcp.ClientSession {
	rs, err := wiring.LoadRoleSession(dir, role)
	ctx.Expect(err).To(specs.BeNil())
	job, _, err := w.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	token, err := w.tokens.Issue(jobtoken.For(job, nil, rs.Role))
	ctx.Expect(err).To(specs.BeNil())
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             w.url + "/worker/jobs/j-1/mcp",
		HTTPClient:           &http.Client{Transport: bearer(token)},
		DisableStandaloneSSE: true,
	}, nil)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call calls tool with args and returns its text and whether it is a tool
// error.
func call(ctx *specs.Context, cs *mcp.ClientSession, tool, args string) (string, bool) {
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: json.RawMessage(args)})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(len(res.Content)).ToEqual(1)
	text, ok := res.Content[0].(*mcp.TextContent)
	ctx.Expect(ok).ToEqual(true)
	return text.Text, res.IsError
}

// feed lists the names of j-1's saved events.
func (w workerWorld) feed(ctx *specs.Context) []string {
	hist, err := w.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	names := []string{}
	for _, r := range hist {
		names = append(names, r.Event.EventName())
	}
	return names
}

// onlySubmits is a configuration root with a role whose tools list only
// submit_result.
func onlySubmits(ctx *specs.Context) string {
	dir := configRoot(ctx, "claude", "opus")
	write(ctx, dir, "roles/only-submits.md", "Build the spec.\n")
	write(ctx, dir, "roles/only-submits.yaml", "name: only-submits\nkind: build\nprompt: roles/only-submits.md\nmode: blocking\nagent: claude\nmodel: opus\ntools: [submit_result]\n")
	return dir
}

// noTools is a configuration root with a build role that lists no tools.
func noTools(ctx *specs.Context) string {
	dir := configRoot(ctx, "claude", "opus")
	write(ctx, dir, "roles/no-tools.md", "Build the spec.\n")
	write(ctx, dir, "roles/no-tools.yaml", "name: no-tools\nkind: build\nprompt: roles/no-tools.md\nmode: blocking\nagent: claude\nmodel: opus\n")
	return dir
}

func TestWorkerTools(t *testing.T) {
	specs.Describe(t, "the worker tools a role may call", func(s *specs.Spec) {
		s.When("a role's tools do not list the tool it calls", func(s *specs.Spec) {
			s.It("refuses the call with an error naming the tool and the role, and records nothing", func(ctx *specs.Context) {
				w := newWorkerWorld(ctx)
				cs := w.session(ctx, onlySubmits(ctx), "only-submits")

				text, isErr := call(ctx, cs, mcpworker.ToolProgress, `{"message":"sneaking in"}`)

				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(text).ToEqual(`refused (403): worker: progress: job token not allowed: role "only-submits" may not use tool "progress"`)
				ctx.Expect(w.feed(ctx)).To(specs.BeEmpty())
			})

			s.It("refuses it even though the agent is shown the tool", func(ctx *specs.Context) {
				w := newWorkerWorld(ctx)
				cs := w.session(ctx, onlySubmits(ctx), "only-submits")
				res, err := cs.ListTools(context.Background(), nil)
				ctx.Expect(err).To(specs.BeNil())
				names := []string{}
				for _, tool := range res.Tools {
					names = append(names, tool.Name)
				}
				ctx.Expect(names).To(specs.Contain(mcpworker.ToolProgress))

				_, isErr := call(ctx, cs, mcpworker.ToolProgress, `{"message":"sneaking in"}`)

				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(w.feed(ctx)).To(specs.BeEmpty())
			})

			s.It("still takes the tools it lists", func(ctx *specs.Context) {
				w := newWorkerWorld(ctx)
				cs := w.session(ctx, onlySubmits(ctx), "only-submits")

				_, isErr := call(ctx, cs, mcpworker.ToolSubmitResult, `{"status":"READY"}`)

				ctx.Expect(isErr).ToEqual(false)
				ctx.Expect(w.feed(ctx)).ToEqual([]string{"StageEntered"})
			})
		})

		s.It("lets a role without a tools list report progress and submit its result", func(ctx *specs.Context) {
			w := newWorkerWorld(ctx)
			cs := w.session(ctx, noTools(ctx), "no-tools")

			_, progressErr := call(ctx, cs, mcpworker.ToolProgress, `{"message":"running the tests"}`)
			_, submitErr := call(ctx, cs, mcpworker.ToolSubmitResult, `{"status":"READY"}`)

			ctx.Expect(progressErr).ToEqual(false)
			ctx.Expect(submitErr).ToEqual(false)
			ctx.Expect(w.feed(ctx)).ToEqual([]string{"AgentProgressed", "StageEntered"})
		})

		s.It("lets the repo's builder report progress and submit its result", func(ctx *specs.Context) {
			w := newWorkerWorld(ctx)
			cs := w.session(ctx, repoRoot, "builder")

			_, progressErr := call(ctx, cs, mcpworker.ToolProgress, `{"message":"running the tests"}`)
			_, submitErr := call(ctx, cs, mcpworker.ToolSubmitResult, `{"status":"READY"}`)

			ctx.Expect(progressErr).ToEqual(false)
			ctx.Expect(submitErr).ToEqual(false)
			ctx.Expect(w.feed(ctx)).ToEqual([]string{"AgentProgressed", "StageEntered"})
		})

		s.When("a session runs the job's checks", func(s *specs.Spec) {
			s.It("runs them for the repo's builder and answers their report", func(ctx *specs.Context) {
				w := newWorkerWorld(ctx)
				cs := w.session(ctx, repoRoot, "builder")

				text, isErr := call(ctx, cs, mcpworker.ToolRunGate, `{}`)

				ctx.Expect(isErr).ToEqual(false)
				ctx.Expect(text).To(specs.StartWith("checks pass for job j-1: `make check` exited 0"))
				ctx.Expect(w.gate.checked()).ToEqual([]domain.JobID{"j-1"})
				ctx.Expect(w.feed(ctx)).To(specs.BeEmpty())
			})

			for _, r := range []struct{ name, role string }{
				{"the repo's reviewer-code, which lists no tools", "reviewer-code"},
				{"the repo's approver, whose tools do not list run_gate", "approver"},
			} {
				s.It("refuses "+r.name+", running no check", func(ctx *specs.Context) {
					w := newWorkerWorld(ctx)
					cs := w.session(ctx, repoRoot, r.role)

					text, isErr := call(ctx, cs, mcpworker.ToolRunGate, `{}`)

					ctx.Expect(isErr).ToEqual(true)
					ctx.Expect(text).ToEqual(`refused (403): worker: run gate: job token not allowed: role "` + r.role + `" may not use tool "run_gate"`)
					ctx.Expect(w.gate.checked()).To(specs.BeEmpty())
				})
			}

			s.It("refuses a build role without a tools list, running no check", func(ctx *specs.Context) {
				w := newWorkerWorld(ctx)
				cs := w.session(ctx, noTools(ctx), "no-tools")
				_, isErr := call(ctx, cs, mcpworker.ToolRunGate, `{}`)
				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(w.gate.checked()).To(specs.BeEmpty())
			})

			s.It("refuses a role whose tools do not list run_gate, running no check", func(ctx *specs.Context) {
				w := newWorkerWorld(ctx)
				cs := w.session(ctx, onlySubmits(ctx), "only-submits")
				_, isErr := call(ctx, cs, mcpworker.ToolRunGate, `{}`)
				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(w.gate.checked()).To(specs.BeEmpty())
			})
		})

		s.When("a session asks a person a question", func(s *specs.Spec) {
			const question = `{"question":"Dark mode for everyone?","options":["yes","beta only"],"to":"product"}`
			for _, r := range []struct{ role, stage, wait string }{
				{"spec-writer", "spec", domain.StageNeedsInfo},
				{"approver", "approve", domain.StageNeedsHuman},
			} {
				s.It("takes it from the repo's "+r.role+", leaving the job in "+r.wait, func(ctx *specs.Context) {
					w := newWorkerWorldIn(ctx, r.stage)
					cs := w.session(ctx, repoRoot, r.role)

					_, isErr := call(ctx, cs, mcpworker.ToolAskHuman, question)

					ctx.Expect(isErr).ToEqual(false)
					ctx.Expect(w.feed(ctx)[0]).ToEqual("HumanAsked")
					job, _, err := w.store.Load(context.Background(), "j-1")
					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(job.Stage).ToEqual(r.wait)
				})
			}

			for _, r := range []struct{ role, stage string }{{"builder", "build"}, {"reviewer-code", "review"}} {
				s.It("refuses the repo's "+r.role+", recording nothing", func(ctx *specs.Context) {
					w := newWorkerWorldIn(ctx, r.stage)
					cs := w.session(ctx, repoRoot, r.role)

					text, isErr := call(ctx, cs, mcpworker.ToolAskHuman, question)

					ctx.Expect(isErr).ToEqual(true)
					ctx.Expect(text).ToEqual(`refused (403): worker: ask human: job token not allowed: role "` + r.role + `" may not use tool "ask_human"`)
					ctx.Expect(w.feed(ctx)).To(specs.BeEmpty())
				})
			}
		})

		s.It("refuses progress arguments over 1 MiB and records nothing", func(ctx *specs.Context) {
			w := newWorkerWorld(ctx)
			cs := w.session(ctx, repoRoot, "builder")

			text, isErr := call(ctx, cs, mcpworker.ToolProgress, `{"message":"`+strings.Repeat("x", mcpworker.MaxResultBytes)+`"}`)

			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual("refused (413): progress arguments over 1 MiB")
			ctx.Expect(w.feed(ctx)).To(specs.BeEmpty())
		})
	})
}
