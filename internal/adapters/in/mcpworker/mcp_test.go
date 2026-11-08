package mcpworker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// withAuth sets an Authorization header on every request, as an agent's
// MCP client does with the headers its session was given.
type withAuth struct{ auth string }

func (a withAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if a.auth != "" {
		r.Header.Set("Authorization", a.auth)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// connect opens an MCP session with job's server on h, sending auth.
func connect(ctx *specs.Context, h http.Handler, job, auth string) *mcp.ClientSession {
	srv := httptest.NewServer(h)
	ctx.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/worker/jobs/" + job + "/mcp",
		HTTPClient:           &http.Client{Transport: withAuth{auth}},
		DisableStandaloneSSE: true,
	}, nil)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Cleanup(func() { _ = cs.Close() })
	return cs
}

// submitTool calls submit_result with args and returns its text and
// whether it is a tool error.
func submitTool(ctx *specs.Context, cs *mcp.ClientSession, args string) (string, bool) {
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolSubmitResult, Arguments: json.RawMessage(args)})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(len(res.Content)).ToEqual(1)
	text, ok := res.Content[0].(*mcp.TextContent)
	ctx.Expect(ok).ToEqual(true)
	return text.Text, res.IsError
}

// progressTool calls progress with args and returns its text and whether
// it is a tool error.
func progressTool(ctx *specs.Context, cs *mcp.ClientSession, args string) (string, bool) {
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolProgress, Arguments: json.RawMessage(args)})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(len(res.Content)).ToEqual(1)
	text, ok := res.Content[0].(*mcp.TextContent)
	ctx.Expect(ok).ToEqual(true)
	return text.Text, res.IsError
}

func TestMCP(t *testing.T) {
	specs.Describe(t, "the worker MCP server", func(s *specs.Spec) {
		s.It("offers ask_human, dev_server, progress, run_gate and submit_result as its only tools", func(ctx *specs.Context) {
			cs := connect(ctx, mcpworker.Handler(&submitter{}), "j-1", "Bearer tok")
			res, err := cs.ListTools(context.Background(), nil)
			ctx.Expect(err).To(specs.BeNil())
			names := make([]string, len(res.Tools))
			for i, tool := range res.Tools {
				names[i] = tool.Name
			}
			ctx.Expect(names).ToEqual([]string{"ask_human", "dev_server", "progress", "run_gate", "submit_result"})
		})

		s.It("hands the bearer token, the job of its path and the arguments to the submitter and names the saved events", func(ctx *specs.Context) {
			sub := &submitter{}
			cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
			text, isErr := submitTool(ctx, cs, `{"status":"READY"}`)
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual("result recorded for job j-1: StageEntered")
			ctx.Expect(sub.calls).ToEqual([]call{{"tok", "j-1", `{"status":"READY"}`}})
		})

		s.It("hands no token over when the request has no bearer token", func(ctx *specs.Context) {
			sub := &submitter{err: jobtoken.ErrNoToken}
			cs := connect(ctx, mcpworker.Handler(sub), "j-1", "")
			text, isErr := submitTool(ctx, cs, `{}`)
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual("refused (401): no job token")
			ctx.Expect(sub.calls[0].token).ToEqual("")
		})

		s.It("answers a refusal as a tool error worded as the HTTP route words it", func(ctx *specs.Context) {
			err := fmt.Errorf("w: build result: %w: status", domain.ErrInvalidValue)
			cs := connect(ctx, mcpworker.Handler(&submitter{err: err}), "j-1", "Bearer tok")
			text, isErr := submitTool(ctx, cs, `{"status":"DONE"}`)
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual("refused (422): " + err.Error())
		})

		s.It("hides an unexpected error", func(ctx *specs.Context) {
			cs := connect(ctx, mcpworker.Handler(&submitter{err: fmt.Errorf("sqlite: disk I/O at /var/x")}), "j-1", "Bearer tok")
			text, isErr := submitTool(ctx, cs, `{}`)
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual("refused (500): internal error")
		})

		s.It("refuses a result over the size limit without submitting it", func(ctx *specs.Context) {
			sub := &submitter{}
			cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
			text, isErr := submitTool(ctx, cs, `{"summary":"`+strings.Repeat("x", mcpworker.MaxResultBytes)+`"}`)
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual("refused (413): result over 1 MiB")
			ctx.Expect(sub.calls).To(specs.BeEmpty())
		})

		s.It("knows no other tool", func(ctx *specs.Context) {
			sub := &submitter{}
			cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
			_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "job_approve", Arguments: map[string]any{}})
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(sub.calls).To(specs.BeEmpty())
			ctx.Expect(sub.progress).To(specs.BeEmpty())
		})

		s.When("a builder runs the job's checks", func(s *specs.Spec) {
			s.It("hands the bearer token and the job of its path to the submitter and answers the report", func(ctx *specs.Context) {
				sub := &submitter{run: gateRun}
				cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolRunGate, Arguments: map[string]any{}})
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res.IsError).ToEqual(false)
				ctx.Expect(res.Content[0].(*mcp.TextContent).Text).ToEqual(gateReport)
				var g mcpworker.GateRun
				data, err := json.Marshal(res.StructuredContent)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(json.Unmarshal(data, &g)).To(specs.BeNil())
				ctx.Expect(g).ToEqual(mcpworker.GateRun{Job: "j-1", Passed: false, Run: gateRun, Report: gateReport})
				ctx.Expect(sub.gates).ToEqual([]call{{token: "tok", job: "j-1"}})
			})

			s.It("never reads the arguments, so nothing an agent sends reaches the checks", func(ctx *specs.Context) {
				sub := &submitter{run: gateRun}
				cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
				args := map[string]any{"command": []string{"sh", "-c", "exit 0"}, "job": "j-2"}
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolRunGate, Arguments: args})
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res.Content[0].(*mcp.TextContent).Text).ToEqual(gateReport)
				ctx.Expect(sub.gates).ToEqual([]call{{token: "tok", job: "j-1"}})
			})

			s.It("answers a role that may not run the checks with a tool error worded as the HTTP route words it", func(ctx *specs.Context) {
				err := fmt.Errorf("worker: run gate: %w: role %q may not use tool %q", jobtoken.ErrForbidden, "reviewer-code", "run_gate")
				cs := connect(ctx, mcpworker.Handler(&submitter{err: err}), "j-1", "Bearer tok")
				res, callErr := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolRunGate, Arguments: map[string]any{}})
				ctx.Expect(callErr).To(specs.BeNil())
				ctx.Expect(res.IsError).ToEqual(true)
				ctx.Expect(res.Content[0].(*mcp.TextContent).Text).ToEqual("refused (403): " + err.Error())
			})
		})

		s.When("an agent asks a person a question", func(s *specs.Spec) {
			const question = `{"question":"Dark mode for everyone?","options":["yes","beta only"],"to":"product"}`

			s.It("hands the bearer token, the job of its path and the arguments to the submitter, as POST AskPath does, and tells it to end its turn", func(ctx *specs.Context) {
				sub := &submitter{}
				cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolAskHuman, Arguments: json.RawMessage(question)})
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res.IsError).ToEqual(false)
				ctx.Expect(res.Content[0].(*mcp.TextContent).Text).ToEqual("question recorded for job j-1: HumanAsked, NeedsInfo. " +
					"The job waits for a person's answer and your stage runs again with it; hand in no result and end your turn.\n")
				ctx.Expect(sub.asks).ToEqual([]call{{"tok", "j-1", question}})
				ctx.Expect(sub.calls).To(specs.BeEmpty())
			})

			s.It("answers a role that may not ask with a tool error worded as the HTTP route words it", func(ctx *specs.Context) {
				err := fmt.Errorf("worker: ask human: %w: role %q may not use tool %q", jobtoken.ErrForbidden, "builder", "ask_human")
				cs := connect(ctx, mcpworker.Handler(&submitter{err: err}), "j-1", "Bearer tok")
				res, callErr := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolAskHuman, Arguments: json.RawMessage(question)})
				ctx.Expect(callErr).To(specs.BeNil())
				ctx.Expect(res.IsError).ToEqual(true)
				ctx.Expect(res.Content[0].(*mcp.TextContent).Text).ToEqual("refused (403): " + err.Error())
			})
		})

		s.When("an agent reports progress", func(s *specs.Spec) {
			s.It("hands the bearer token, the job of its path and the message to the submitter and confirms it", func(ctx *specs.Context) {
				sub := &submitter{}
				cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
				text, isErr := progressTool(ctx, cs, `{"message":"running the tests"}`)
				ctx.Expect(isErr).ToEqual(false)
				ctx.Expect(text).ToEqual("progress recorded for job j-1: AgentProgressed")
				ctx.Expect(sub.progress).ToEqual([]call{{"tok", "j-1", "running the tests"}})
				ctx.Expect(sub.calls).To(specs.BeEmpty())
			})

			s.It("answers a refusal as a tool error the agent can read", func(ctx *specs.Context) {
				err := fmt.Errorf("worker: progress: decide: progress without a message: %w", domain.ErrMissingField)
				cs := connect(ctx, mcpworker.Handler(&submitter{err: err}), "j-1", "Bearer tok")
				text, isErr := progressTool(ctx, cs, `{"message":""}`)
				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(text).ToEqual("refused (422): " + err.Error())
			})

			s.It("refuses arguments over the size limit without handing them over", func(ctx *specs.Context) {
				sub := &submitter{}
				cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
				text, isErr := progressTool(ctx, cs, `{"message":"`+strings.Repeat("x", mcpworker.MaxResultBytes)+`"}`)
				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(text).ToEqual("refused (413): progress arguments over 1 MiB")
				ctx.Expect(sub.progress).To(specs.BeEmpty())
			})

			for _, args := range []string{`{}`, `{"message":7}`, `{"message":"x","role":"approver"}`} {
				s.It("refuses arguments "+args+" without handing them over", func(ctx *specs.Context) {
					sub := &submitter{}
					cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
					text, isErr := progressTool(ctx, cs, args)
					ctx.Expect(isErr).ToEqual(true)
					ctx.Expect(text).ToEqual(`refused (422): arguments must be {"message": "<text>"}`)
					ctx.Expect(sub.progress).To(specs.BeEmpty())
				})
			}
		})

		s.When("an agent calls dev_server", func(s *specs.Spec) {
			s.It("starts the token's dev server on the job of its path and answers its port", func(ctx *specs.Context) {
				sub := &submitter{}
				cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
				text, isErr := devServerTool(ctx, cs, `{"action":"start"}`)
				ctx.Expect(isErr).ToEqual(false)
				ctx.Expect(text).ToEqual("dev server of job j-1 on port 5100: http://localhost:5100\n")
				ctx.Expect(sub.devs).ToEqual([]call{{"tok", "j-1", "start"}})
			})

			s.It("stops it when asked", func(ctx *specs.Context) {
				sub := &submitter{}
				cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
				text, isErr := devServerTool(ctx, cs, `{"action":"stop"}`)
				ctx.Expect(isErr).ToEqual(false)
				ctx.Expect(text).ToEqual("dev server of job j-1 stopped\n")
				ctx.Expect(sub.devs).ToEqual([]call{{"tok", "j-1", "stop"}})
			})

			s.It("answers a role that may not run it with a tool error worded as the HTTP route words it", func(ctx *specs.Context) {
				err := fmt.Errorf("worker: dev server: %w: role \"reviewer-security\" may not use tool \"dev_server\"", jobtoken.ErrForbidden)
				cs := connect(ctx, mcpworker.Handler(&submitter{err: err}), "j-1", "Bearer tok")
				text, isErr := devServerTool(ctx, cs, `{"action":"start"}`)
				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(text).ToEqual("refused (403): " + err.Error())
			})

			for _, args := range []string{`{}`, `{"action":"restart"}`, `{"action":"start","command":"sh -c evil"}`} {
				s.It("refuses arguments "+args+" without handing them over", func(ctx *specs.Context) {
					sub := &submitter{}
					cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
					text, isErr := devServerTool(ctx, cs, args)
					ctx.Expect(isErr).ToEqual(true)
					ctx.Expect(text).ToEqual(`refused (422): arguments must be {"action": "start"} or {"action": "stop"}`)
					ctx.Expect(sub.devs).To(specs.BeEmpty())
				})
			}
		})
	})
}

// devServerTool calls dev_server with args and returns its text and
// whether it is a tool error.
func devServerTool(ctx *specs.Context, cs *mcp.ClientSession, args string) (string, bool) {
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolDevServer, Arguments: json.RawMessage(args)})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(len(res.Content)).ToEqual(1)
	text, ok := res.Content[0].(*mcp.TextContent)
	ctx.Expect(ok).ToEqual(true)
	return text.Text, res.IsError
}
