package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pablogore/software-factory/internal/domain"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// tool calls submit_result on job's MCP server with token and result, as an
// agent with MCP would, and returns the tool's text and whether it is an
// error.
func (f factoryd) tool(ctx *specs.Context, job domain.JobID, token, result string) (string, bool) {
	text, isErr, err := f.callTool(job, token, result)
	ctx.Expect(err).To(specs.BeNil())
	return text, isErr
}

func (f factoryd) callTool(job domain.JobID, token, result string) (string, bool, error) {
	return f.call("submit_result", job, token, result)
}

// call calls tool on job's MCP server with token and args.
func (f factoryd) call(tool string, job domain.JobID, token, args string) (string, bool, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             f.url + "/worker/jobs/" + string(job) + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: json.RawMessage(args)})
	if err != nil {
		return "", false, err
	}
	if len(res.Content) != 1 {
		return "", false, fmt.Errorf("%d contents", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return "", false, fmt.Errorf("content %T", res.Content[0])
	}
	return text.Text, res.IsError, nil
}

// cliError is the refusal the CLI printed, without its command prefix.
func cliError(r run) string {
	return strings.TrimSpace(strings.TrimPrefix(r.stderr, "factory worker submit-result: "))
}

type tokenRow struct {
	name  string
	token func(ctx *specs.Context, f factoryd) string
}

func TestSubmitResultTool(t *testing.T) {
	specs.Describe(t, "the MCP tool submit_result", func(s *specs.Spec) {
		s.It("records a valid result of the token's kind as the job's result, as the CLI does", func(ctx *specs.Context) {
			f := start(ctx)
			text, isErr := f.tool(ctx, "j-1", f.tokens["j-1"], `{"status":"READY","summary":"done"}`)
			r := cli(f.env("j-2", f.tokens["j-2"]), `{"status":"READY","summary":"done"}`, "submit-result")
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual("result recorded for job j-1: StageEntered")
			ctx.Expect(r.stdout).ToEqual("result recorded for job j-2: StageEntered\n")
			ctx.Expect(f.events(ctx, "j-1")).ToEqual(f.events(ctx, "j-2"))
			job, _, err := f.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual(domain.StageGate)
		})

		s.It("refuses a result invalid for the kind with the CLI's error, records nothing and keeps the token for a retry", func(ctx *specs.Context) {
			f := start(ctx)
			text, isErr := f.tool(ctx, "j-1", f.tokens["j-1"], `{"status":"DONE"}`)
			r := cli(f.env("j-1", f.tokens["j-1"]), `{"status":"DONE"}`, "submit-result")
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual(cliError(r))
			ctx.Expect(text).To(specs.Contain(`refused (422): worker: build result: invalid value: status "DONE"`))
			ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())

			text, isErr = f.tool(ctx, "j-1", f.tokens["j-1"], `{"status":"READY"}`)
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual("result recorded for job j-1: StageEntered")
		})

		rows := []tokenRow{
			{"another job's token", func(_ *specs.Context, f factoryd) string { return f.tokens["j-2"] }},
			{"an unknown token", func(*specs.Context, factoryd) string { return "0123abcd" }},
			{"a token of an earlier round", func(ctx *specs.Context, f factoryd) string {
				for _, ev := range []domain.Event{
					domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at},
					domain.GateFailed{Job: "j-1", Run: domain.ChecksRun{Command: "make check", ExitCode: 2}, At: at},
				} {
					_, v, err := f.store.Load(context.Background(), "j-1")
					ctx.Expect(err).To(specs.BeNil())
					p, _ := onePipeline{}.Pipeline(context.Background(), "default")
					_, _, err = f.store.Apply(context.Background(), "j-1", v, p, ev)
					ctx.Expect(err).To(specs.BeNil())
				}
				return f.tokens["j-1"]
			}},
			{"a token already used", func(ctx *specs.Context, f factoryd) string {
				ctx.Expect(cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY"}`, "submit-result").code).ToEqual(0)
				return f.tokens["j-1"]
			}},
		}
		specs.Table(s, rows, func(r tokenRow) string { return "refuses " + r.name + " with the CLI's error and records nothing" },
			func(ctx *specs.Context, row tokenRow) {
				f := start(ctx)
				token := row.token(ctx, f)
				before := f.events(ctx, "j-1")
				r := cli(f.env("j-1", token), `{"status":"READY"}`, "submit-result")
				text, isErr := f.tool(ctx, "j-1", token, `{"status":"READY"}`)
				ctx.Expect(r.code).ToEqual(1)
				ctx.Expect(isErr).ToEqual(true)
				ctx.Expect(text).ToEqual(cliError(r))
				ctx.Expect(f.events(ctx, "j-1")).ToEqual(before)
				ctx.Expect(f.events(ctx, "j-2")).To(specs.BeEmpty())
			})

		s.It("refuses by the CLI a token whose result the tool recorded", func(ctx *specs.Context) {
			f := start(ctx)
			_, isErr := f.tool(ctx, "j-1", f.tokens["j-1"], `{"status":"READY"}`)
			ctx.Expect(isErr).ToEqual(false)
			r := cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY"}`, "submit-result")
			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(cliError(r)).ToEqual("refused (401): worker: submit result: unknown job token")
			ctx.Expect(f.events(ctx, "j-1")).ToEqual([]string{"StageEntered"})
		})

		s.It("refuses by the tool a token whose result the CLI recorded", func(ctx *specs.Context) {
			f := start(ctx)
			ctx.Expect(cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY"}`, "submit-result").code).ToEqual(0)
			text, isErr := f.tool(ctx, "j-1", f.tokens["j-1"], `{"status":"READY"}`)
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual("refused (401): worker: submit result: unknown job token")
			ctx.Expect(f.events(ctx, "j-1")).ToEqual([]string{"StageEntered"})
		})

		s.It("records one result when the tool and the CLI hand results over with the same token at once", func(ctx *specs.Context) {
			f := start(ctx)
			const each = 5
			var wg sync.WaitGroup
			var mu sync.Mutex
			recorded, errs := 0, []error{}
			for range each {
				wg.Go(func() {
					_, isErr, err := f.callTool("j-1", f.tokens["j-1"], `{"status":"READY"}`)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						errs = append(errs, err)
					} else if !isErr {
						recorded++
					}
				})
				wg.Go(func() {
					r := cli(f.env("j-1", f.tokens["j-1"]), `{"status":"READY"}`, "submit-result")
					mu.Lock()
					defer mu.Unlock()
					if r.code == 0 {
						recorded++
					}
				})
			}
			wg.Wait()
			ctx.Expect(errs).To(specs.BeEmpty())
			ctx.Expect(recorded).ToEqual(1)
			ctx.Expect(f.events(ctx, "j-1")).ToEqual([]string{"StageEntered"})
		})
	})
}

// progress calls progress on job's MCP server with token and message.
func (f factoryd) progress(ctx *specs.Context, job domain.JobID, token, message string) (string, bool) {
	args, err := json.Marshal(map[string]string{"message": message})
	ctx.Expect(err).To(specs.BeNil())
	text, isErr, err := f.call("progress", job, token, string(args))
	ctx.Expect(err).To(specs.BeNil())
	return text, isErr
}

// progressed lists the role and text of each progress message job id saved.
func (f factoryd) progressed(ctx *specs.Context, id domain.JobID) []string {
	hist, err := f.store.History(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	msgs := []string{}
	for _, h := range hist {
		if p, ok := h.Event.(domain.AgentProgressed); ok {
			msgs = append(msgs, p.Role+": "+p.Text)
		}
	}
	return msgs
}

func TestProgressTool(t *testing.T) {
	specs.Describe(t, "the MCP tool progress", func(s *specs.Spec) {
		s.It("records each message as an event of the job, with the token's role, in order, and submit_result still works", func(ctx *specs.Context) {
			f := start(ctx)
			text, isErr := f.progress(ctx, "j-1", f.tokens["j-1"], "reading the spec")
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual("progress recorded for job j-1: AgentProgressed")
			_, isErr = f.progress(ctx, "j-1", f.tokens["j-1"], "running the tests")
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(f.progressed(ctx, "j-1")).ToEqual([]string{"builder: reading the spec", "builder: running the tests"})
			ctx.Expect(f.events(ctx, "j-2")).To(specs.BeEmpty())

			text, isErr = f.tool(ctx, "j-1", f.tokens["j-1"], `{"status":"READY"}`)
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual("result recorded for job j-1: StageEntered")
			ctx.Expect(f.events(ctx, "j-1")).ToEqual([]string{"AgentProgressed", "AgentProgressed", "StageEntered"})
		})

		s.It("refuses an empty message with an error the agent reads, and records nothing", func(ctx *specs.Context) {
			f := start(ctx)
			text, isErr := f.progress(ctx, "j-1", f.tokens["j-1"], "")
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).To(specs.StartWith("refused (422): worker: progress: "))
			ctx.Expect(text).To(specs.Contain("progress without a message: missing field"))
			ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
		})

		s.It("refuses a message over the longest length with an error the agent reads, and records nothing", func(ctx *specs.Context) {
			f := start(ctx)
			text, isErr := f.progress(ctx, "j-1", f.tokens["j-1"], strings.Repeat("x", domain.MaxProgressText+1))
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).To(specs.StartWith("refused (422): worker: progress: "))
			ctx.Expect(text).To(specs.Contain(fmt.Sprintf("invalid value: message of %d characters, over %d", domain.MaxProgressText+1, domain.MaxProgressText)))
			ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
		})

		s.It("refuses another job's token and records nothing", func(ctx *specs.Context) {
			f := start(ctx)
			text, isErr := f.progress(ctx, "j-1", f.tokens["j-2"], "hello")
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).To(specs.StartWith("refused (403): "))
			ctx.Expect(f.events(ctx, "j-1")).To(specs.BeEmpty())
			ctx.Expect(f.events(ctx, "j-2")).To(specs.BeEmpty())
		})
	})
}
