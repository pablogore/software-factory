package wiring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/wiring"
)

// TestMain lets this test binary be the fake agent serve launches.
func TestMain(m *testing.M) {
	if acpfake.Requested() {
		os.Exit(acpfake.Run())
	}
	os.Exit(m.Run())
}

// fakeAgents runs the n-th session serve launches on the fake ACP agent
// playing scripts[n], recording to a file of its own; a session past the
// last script plays an empty one.
type fakeAgents struct {
	dir     string
	scripts []acpfake.Script

	mu    sync.Mutex
	roles []string
}

func (f *fakeAgents) agents(clock ports.Clock) wiring.Agents {
	return func(rs wiring.RoleSession) ports.Agent {
		f.mu.Lock()
		n := len(f.roles)
		f.roles = append(f.roles, rs.Role.Name)
		f.mu.Unlock()
		var sc acpfake.Script
		if n < len(f.scripts) {
			sc = f.scripts[n]
		}
		cfg, err := acpfake.Config(sc, f.record(n))
		if err != nil {
			return failingAgent{err}
		}
		return acp.New(cfg, clock)
	}
}

func (f *fakeAgents) record(n int) string {
	return filepath.Join(f.dir, fmt.Sprintf("session-%d.json", n))
}

// launched are the roles of the sessions launched so far.
func (f *fakeAgents) launched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.roles...)
}

// received is what the n-th session's fake agent got.
func (f *fakeAgents) received(ctx *specs.Context, n int) acpfake.Received {
	got, err := acpfake.Load(f.record(n))
	ctx.Expect(err).To(specs.BeNil())
	return got
}

// submitted is what the n-th session's fake agent got once it recorded a
// submit's answer. The fake records it after factoryd saved the result, so
// a test that saw the job move on may read the file before it does.
func (f *fakeAgents) submitted(ctx *specs.Context, n int) acpfake.Received {
	deadline := time.Now().Add(20 * time.Second)
	got := f.received(ctx, n)
	for len(got.Submitted) == 0 {
		if time.Now().After(deadline) {
			ctx.Testing().Fatal("the session never recorded its submit")
		}
		time.Sleep(20 * time.Millisecond)
		got = f.received(ctx, n)
	}
	return got
}

type failingAgent struct{ err error }

func (a failingAgent) Start(context.Context, ports.SessionSpec) (ports.Session, error) {
	return nil, a.err
}

// syncBuffer is a log the server's goroutines may write at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// launchPipeline is the repo's default pipeline as decide needs it to seed
// a job.
var launchPipeline = domain.Pipeline{Name: "default", Stages: []domain.Stage{
	{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
	{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
	{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
	{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
}}

// launchRepo is a new git repo on main with one commit, the repo jobs'
// worktrees are made in.
func launchRepo(ctx *specs.Context) string {
	dir := ctx.Testing().TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		ctx.Expect(err).To(specs.BeNil())
		ctx.Expect(string(out)).ToEqual("")
	}
	return dir
}

// launchConfig serves the repo's roles and pipelines over a store in the
// test's directory and a git repo of its own, with sessions on agents.
func launchConfig(ctx *specs.Context, clk *tickClock, agents *fakeAgents, log *syncBuffer) wiring.ServeConfig {
	dir := ctx.Testing().TempDir()
	return wiring.ServeConfig{
		Config: "../..", Store: filepath.Join(dir, "factoryd.db"), Repo: launchRepo(ctx), Base: "main",
		DevState: filepath.Join(dir, "devservers"), Homes: childenv.Homes{Root: filepath.Join(dir, "homes")}, Concurrency: 2, Budget: 5, Costs: wiring.CostsSubscription,
		Listen: "127.0.0.1:0", Agents: agents.agents(clk),
		SessionTimeout: serveSessionTimeout, Tick: serveTick, Grace: serveGrace, Clock: clk, Log: log,
	}
}

// seedBuild saves each of ids past specgate, so its entry into build waits
// for the launcher.
func seedBuild(ctx *specs.Context, path string, ids ...domain.JobID) {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	defer func() { ctx.Expect(st.Close()).To(specs.BeNil()) }()
	for _, id := range ids {
		ctx.Expect(st.Create(bg, domain.Job{ID: id, Title: "Job", Pipeline: "default", Stage: domain.StageSpecgate, Round: 1})).To(specs.BeNil())
		_, v, err := st.Load(bg, id)
		ctx.Expect(err).To(specs.BeNil())
		_, _, err = st.Apply(bg, id, v, launchPipeline, domain.SpecgatePassed{Job: id, At: serveEpoch})
		ctx.Expect(err).To(specs.BeNil())
	}
}

// history names id's saved events, with the stage of each StageEntered.
func history(ctx *specs.Context, path string, id domain.JobID) []string {
	hist, err := reopen(ctx, path).History(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	names := []string{}
	for _, r := range hist {
		name := r.Event.EventName()
		if e, ok := r.Event.(domain.StageEntered); ok {
			name += "(" + e.Stage + ")"
		}
		names = append(names, name)
	}
	return names
}

// tickUntil moves the fake clock a tick at a time, while the server waits
// on it, until done holds; it fails the test rather than hang.
func tickUntil(ctx *specs.Context, clk *tickClock, done func() bool) {
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			ctx.Testing().Fatal("the server never got there")
		}
		waitAsked(ctx, clk)
		// Sessions run on real time, between ticks.
		time.Sleep(20 * time.Millisecond)
		clk.Advance(serveTick)
	}
}

// serverWaitAsked is the next wait the server asks the clock for, past the
// sessions' time limits, which each session asks for as it starts.
func serverWaitAsked(ctx *specs.Context, c *tickClock) time.Duration {
	for {
		if d := waitAsked(ctx, c); d != serveSessionTimeout {
			return d
		}
	}
}

// bearerOf is the token the session's agent was handed with its MCP server.
func bearerOf(ctx *specs.Context, got acpfake.Received) (string, string) {
	ctx.Expect(len(got.MCPServers)).ToEqual(1)
	srv := got.MCPServers[0]
	ctx.Expect(len(srv.Headers)).ToEqual(1)
	ctx.Expect(srv.Headers[0].Name).ToEqual("Authorization")
	token, ok := strings.CutPrefix(srv.Headers[0].Value, "Bearer ")
	ctx.Expect(ok).To(specs.BeTrue())
	return srv.URL, token
}

// connect opens an MCP connection to url with token, as an agent's MCP
// client does.
func connect(ctx *specs.Context, url, token string) *mcp.ClientSession {
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: url, HTTPClient: &http.Client{Transport: bearer(token)}, DisableStandaloneSSE: true,
	}, nil)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Cleanup(func() { _ = cs.Close() })
	return cs
}

var ready = json.RawMessage(`{"status":"READY","summary":"built"}`)

func TestDemoLaunch(t *testing.T) {
	specs.Describe(t, "factoryd serve launching role sessions", func(s *specs.Spec) {
		s.It("relaunches a builder whose first attempt fails, and its second hands in so the job moves on to the gate", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
				// Two turns without a result: the prompt and its one retry.
				{},
				{Turns: []acpfake.Turn{{Submit: ready}}},
			}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1")
			srv, err := wiring.StartServer(context.Background(), cfg)
			ctx.Expect(err).To(specs.BeNil())
			run, stop := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Run(run) }()

			tickUntil(ctx, clk, func() bool {
				job, _, err := reopen(ctx, cfg.Store).Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				return job.Stage == domain.StageGate
			})

			ctx.Expect(history(ctx, cfg.Store, "j-1")).ToEqual([]string{
				"StageEntered(build)", "AttemptFailed", "AttemptRetried", "StageEntered(gate)",
			})
			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "builder"})
			prompt, err := os.ReadFile("../../roles/builder.md")
			ctx.Expect(err).To(specs.BeNil())
			first, second := agents.received(ctx, 0), agents.submitted(ctx, 1)
			ctx.Expect(len(first.Prompts)).ToEqual(2)
			ctx.Expect(first.Prompts[0]).ToEqual(string(prompt))
			ctx.Expect(second.Prompts).ToEqual([]string{string(prompt)})
			ctx.Expect(second.Submitted).ToEqual([]string{"result recorded for job j-1: StageEntered"})
			firstURL, firstToken := bearerOf(ctx, first)
			secondURL, secondToken := bearerOf(ctx, second)
			ctx.Expect(firstURL).ToEqual(srv.Addr() + "/worker/jobs/j-1/mcp")
			ctx.Expect(secondURL).ToEqual(firstURL)
			ctx.Expect(secondToken).To(specs.Not(specs.Equal(firstToken)))
			wt := filepath.Join(cfg.Repo, ".worktrees", "j-1")
			ctx.Expect(first.Cwd).ToEqual(wt)
			ctx.Expect(second.Cwd).ToEqual(wt)

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})
	})
}

func TestDemoTimeoutRetryListen(t *testing.T) {
	specs.Describe(t, "factoryd serve with a real agent's failures and an open address", func(s *specs.Spec) {
		s.It("cuts a session past its time limit as a failed attempt and retries it in a new session, which hands in", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
				{Turns: []acpfake.Turn{{Hang: true}}},
				{Turns: []acpfake.Turn{{Submit: ready}}},
			}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1")
			stop, done := running(ctx, cfg)

			// The hung session asked for its time limit as it started; once
			// it has its prompt, the fake clock runs past it at once.
			asked := waitAsked(ctx, clk)
			for asked != serveSessionTimeout {
				asked = waitAsked(ctx, clk)
			}
			deadline := time.Now().Add(20 * time.Second)
			for len(agents.received(ctx, 0).Prompts) == 0 {
				if time.Now().After(deadline) {
					ctx.Testing().Fatal("the session never got its prompt")
				}
				time.Sleep(20 * time.Millisecond)
			}
			clk.Advance(serveSessionTimeout)
			tickUntil(ctx, clk, func() bool {
				job, _, err := reopen(ctx, cfg.Store).Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				return job.Stage == domain.StageGate
			})

			ctx.Expect(history(ctx, cfg.Store, "j-1")).ToEqual([]string{
				"StageEntered(build)", "AttemptFailed", "AttemptRetried", "StageEntered(gate)",
			})
			hist, err := reopen(ctx, cfg.Store).History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist[1].Event.(domain.AttemptFailed).Reason).ToEqual("the session ran past its time limit of 1h0m0s and was cut")
			ctx.Expect(agents.launched()).ToEqual([]string{"builder", "builder"})
			prompt, err := os.ReadFile("../../roles/builder.md")
			ctx.Expect(err).To(specs.BeNil())
			// Cut, not asked again in the hung session: the retry is a new one.
			ctx.Expect(agents.received(ctx, 0).Prompts).ToEqual([]string{string(prompt)})
			second := agents.submitted(ctx, 1)
			ctx.Expect(second.Prompts).ToEqual([]string{string(prompt)})
			ctx.Expect(second.Submitted).ToEqual([]string{"result recorded for job j-1: StageEntered"})

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		s.It("asks a session once more for its result naming only the MCP tool", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{
				{},
				{Turns: []acpfake.Turn{{Submit: ready}}},
			}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1")
			stop, done := running(ctx, cfg)

			tickUntil(ctx, clk, func() bool {
				job, _, err := reopen(ctx, cfg.Store).Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				return job.Stage == domain.StageGate
			})

			first := agents.received(ctx, 0)
			ctx.Expect(first.Prompts).To(specs.HaveLen(2))
			retry := first.Prompts[1]
			ctx.Expect(retry).To(specs.Contain("submit_result tool of the factoryd MCP server"))
			ctx.Expect(retry).To(specs.Not(specs.Contain("submit-result")))
			ctx.Expect(retry).To(specs.Not(specs.Contain("FACTORY_JOB_TOKEN")))
			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		s.It("serves on an address that is not loopback all the same, warning which one", func(ctx *specs.Context) {
			clk := newTickClock()
			log := &syncBuffer{}
			cfg := launchConfig(ctx, clk, &fakeAgents{dir: ctx.Testing().TempDir()}, log)
			cfg.Listen = "0.0.0.0:0"
			stop, done := running(ctx, cfg)

			ctx.Expect(log.String()).To(specs.Contain("warning: the worker API listens on 0.0.0.0:0 ("))
			ctx.Expect(log.String()).To(specs.Contain("not a loopback address"))
			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		s.It("warns nothing on a loopback address", func(ctx *specs.Context) {
			clk := newTickClock()
			log := &syncBuffer{}
			cfg := launchConfig(ctx, clk, &fakeAgents{dir: ctx.Testing().TempDir()}, log)
			stop, done := running(ctx, cfg)
			stop()
			ctx.Expect(<-done).To(specs.BeNil())
			ctx.Expect(log.String()).To(specs.Not(specs.Contain("warning")))
		})
	})
}

func TestLaunch(t *testing.T) {
	specs.Describe(t, "factoryd serve's session tokens", func(s *specs.Spec) {
		s.It("refuses a session's token on another job, and a result of another role's kind", func(ctx *specs.Context) {
			clk := newTickClock()
			hang := acpfake.Script{Turns: []acpfake.Turn{{Hang: true}}}
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{hang, hang}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedBuild(ctx, cfg.Store, "j-1", "j-2")
			srv, err := wiring.StartServer(context.Background(), cfg)
			ctx.Expect(err).To(specs.BeNil())
			run, stop := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Run(run) }()
			// Both sessions work on until the shutdown cancels them.
			deadline := time.Now().Add(20 * time.Second)
			for len(agents.received(ctx, 0).Prompts) == 0 || len(agents.received(ctx, 1).Prompts) == 0 {
				if time.Now().After(deadline) {
					ctx.Testing().Fatal("the sessions never got their prompts")
				}
				time.Sleep(20 * time.Millisecond)
			}
			urlA, tokenA := bearerOf(ctx, agents.received(ctx, 0))
			urlB, tokenB := bearerOf(ctx, agents.received(ctx, 1))
			ctx.Expect(urlA).To(specs.Not(specs.Equal(urlB)))

			text, isErr := call(ctx, connect(ctx, urlB, tokenA), "submit_result", string(ready))
			ctx.Expect(isErr).To(specs.BeTrue())
			ctx.Expect(text).To(specs.Contain("job token not allowed"))
			text, isErr = call(ctx, connect(ctx, urlA, tokenB), "submit_result", string(ready))
			ctx.Expect(isErr).To(specs.BeTrue())
			ctx.Expect(text).To(specs.Contain("job token not allowed"))
			// The token is a builder's: a reviewer's result is not its to hand in.
			text, isErr = call(ctx, connect(ctx, urlA, tokenA), "submit_result", `{"verdict":"PASS"}`)
			ctx.Expect(isErr).To(specs.BeTrue())
			ctx.Expect(text).To(specs.Contain("build result"))
			for _, id := range []domain.JobID{"j-1", "j-2"} {
				ctx.Expect(history(ctx, cfg.Store, id)).ToEqual([]string{"StageEntered(build)"})
			}

			stop()
			// The first tick's wait comes first; no tick ran, so no other does.
			ctx.Expect(serverWaitAsked(ctx, clk)).ToEqual(serveTick)
			ctx.Expect(serverWaitAsked(ctx, clk)).ToEqual(serveGrace)
			clk.Advance(serveGrace)
			ctx.Expect(<-done).To(specs.MatchError(wiring.ErrShutdownGrace))
		})

		s.It("launches nothing when no cost source is declared", func(ctx *specs.Context) {
			clk := newTickClock()
			agents := &fakeAgents{dir: ctx.Testing().TempDir()}
			log := &syncBuffer{}
			cfg := launchConfig(ctx, clk, agents, log)
			cfg.Costs = ""
			seedBuild(ctx, cfg.Store, "j-1")
			stop, done := running(ctx, cfg)

			tickUntil(ctx, clk, func() bool { return strings.Contains(log.String(), "no cost source configured") })

			ctx.Expect(log.String()).To(specs.Contain("launch: job j-1, role builder: "))
			ctx.Expect(agents.launched()).To(specs.BeEmpty())
			ctx.Expect(history(ctx, cfg.Store, "j-1")).ToEqual([]string{"StageEntered(build)"})
			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})
	})
}
