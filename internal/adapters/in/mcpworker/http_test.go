package mcpworker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// submitter records its calls and answers with err, or with one
// StageEntered for a result, one AgentProgressed for progress and run for
// the checks, and a HumanAsked and a NeedsInfo for a question.
type submitter struct {
	err      error
	calls    []call
	progress []call
	gates    []call
	asks     []call
	run      domain.ChecksRun
	devs     []call
	packs    []call
}

type call struct {
	token string
	job   domain.JobID
	data  string
}

func (s *submitter) Submit(_ context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error) {
	s.calls = append(s.calls, call{token, job, string(data)})
	if s.err != nil {
		return nil, s.err
	}
	return []domain.Event{domain.StageEntered{Job: job, Stage: domain.StageGate, At: time.Time{}}}, nil
}

func (s *submitter) Progress(_ context.Context, token string, job domain.JobID, text string) ([]domain.Event, error) {
	s.progress = append(s.progress, call{token, job, text})
	if s.err != nil {
		return nil, s.err
	}
	return []domain.Event{domain.AgentProgressed{Job: job, Role: "builder", Kind: domain.ProgressMessage, Text: text}}, nil
}

func (s *submitter) RunGate(_ context.Context, token string, job domain.JobID) (domain.ChecksRun, error) {
	s.gates = append(s.gates, call{token: token, job: job})
	if s.err != nil {
		return domain.ChecksRun{}, s.err
	}
	return s.run, nil
}

func (s *submitter) DevServer(_ context.Context, token string, job domain.JobID) (int, error) {
	s.devs = append(s.devs, call{token, job, "start"})
	if s.err != nil {
		return 0, s.err
	}
	return 5100, nil
}

func (s *submitter) StopDevServer(_ context.Context, token string, job domain.JobID) error {
	s.devs = append(s.devs, call{token, job, "stop"})
	return s.err
}

func (s *submitter) AskHuman(_ context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error) {
	s.asks = append(s.asks, call{token, job, string(data)})
	if s.err != nil {
		return nil, s.err
	}
	return []domain.Event{domain.HumanAsked{Job: job}, domain.NeedsInfo{Job: job, From: "spec"}}, nil
}

func (s *submitter) ContextPack(_ context.Context, token string, job domain.JobID) (string, error) {
	s.packs = append(s.packs, call{token: token, job: job})
	if s.err != nil {
		return "", s.err
	}
	return "# Context pack of job \"" + string(job) + "\", round 1\n", nil
}

// gateRun is a failing run of the checks, with a rule the diff broke.
var gateRun = domain.ChecksRun{Command: "make check", ExitCode: 2, Duration: 1500 * time.Millisecond, Output: "--- FAIL: TestSave", Violations: []string{"Makefile is protected"}, Notes: []string{"no test count"}}

// gateReport is gateRun on j-1 as the agent reads it.
const gateReport = "checks fail for job j-1: `make check` exited 2 in 1.5s\n--- output ---\n--- FAIL: TestSave\n--- end of output ---\nrule broken: Makefile is protected\nnote: no test count\n"

func do(h http.Handler, method, path, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func refusal(ctx *specs.Context, rec *httptest.ResponseRecorder) string {
	var r mcpworker.Refused
	ctx.Expect(json.Unmarshal(rec.Body.Bytes(), &r)).To(specs.BeNil())
	return r.Error
}

type statusRow struct {
	name string
	err  error
	code int
}

func TestHandler(t *testing.T) {
	specs.Describe(t, "the worker HTTP API", func(s *specs.Spec) {
		s.It("hands the bearer token, the job and the body to the submitter and names the saved events", func(ctx *specs.Context) {
			sub := &submitter{}
			rec := do(mcpworker.Handler(sub), http.MethodPost, "/worker/jobs/j-1/result", "Bearer tok", `{"status":"READY"}`)
			ctx.Expect(rec.Code).ToEqual(http.StatusOK)
			ctx.Expect(sub.calls).ToEqual([]call{{"tok", "j-1", `{"status":"READY"}`}})
			var a mcpworker.Accepted
			ctx.Expect(json.Unmarshal(rec.Body.Bytes(), &a)).To(specs.BeNil())
			ctx.Expect(a).ToEqual(mcpworker.Accepted{Job: "j-1", Events: []string{"StageEntered"}})
		})

		s.It("hands no token over when the request has no bearer token", func(ctx *specs.Context) {
			sub := &submitter{err: jobtoken.ErrNoToken}
			rec := do(mcpworker.Handler(sub), http.MethodPost, "/worker/jobs/j-1/result", "Basic dG9rOg==", `{}`)
			ctx.Expect(rec.Code).ToEqual(http.StatusUnauthorized)
			ctx.Expect(sub.calls[0].token).ToEqual("")
		})

		rows := []statusRow{
			{"no token", fmt.Errorf("w: %w", jobtoken.ErrNoToken), http.StatusUnauthorized},
			{"an unknown token", fmt.Errorf("w: %w", jobtoken.ErrUnknownToken), http.StatusUnauthorized},
			{"another job's token", fmt.Errorf("w: %w: job %q", jobtoken.ErrForbidden, "j-1"), http.StatusForbidden},
			{"a token from an earlier round", fmt.Errorf("w: %w: issued in round 1", jobtoken.ErrStale), http.StatusConflict},
			{"a result missing a field", fmt.Errorf("w: build result: %w: status", domain.ErrMissingField), http.StatusUnprocessableEntity},
			{"a result with a bad value", fmt.Errorf("w: build result: %w: status", domain.ErrInvalidValue), http.StatusUnprocessableEntity},
			{"a job that does not exist", fmt.Errorf("w: %w", ports.ErrJobNotFound), http.StatusNotFound},
			{"a result the stage does not take", fmt.Errorf("w: %w", decide.ErrIllegalTransition), http.StatusConflict},
			{"a job saved meanwhile", fmt.Errorf("w: %w", ports.ErrVersionConflict), http.StatusConflict},
			{"checks already running for the job", fmt.Errorf("w: run gate: %w: the checks of job %q are running", domain.ErrBusy, "j-1"), http.StatusConflict},
			{"checks cancelled by the job's result", fmt.Errorf("w: run gate: %w", gatelock.ErrPreempted), http.StatusConflict},
			{"a run_gate call past its time limit", fmt.Errorf("w: run gate: %w: the call ran over 15m0s", domain.ErrTimedOut), http.StatusGatewayTimeout},
			{"too many run_gate calls at once", fmt.Errorf("w: run gate: %w: 4 run_gate calls are running", domain.ErrTooMany), http.StatusTooManyRequests},
		}
		specs.Table(s, rows, func(r statusRow) string { return fmt.Sprintf("answers %d with the error to %s", r.code, r.name) },
			func(ctx *specs.Context, r statusRow) {
				rec := do(mcpworker.Handler(&submitter{err: r.err}), http.MethodPost, "/worker/jobs/j-1/result", "Bearer tok", `{}`)
				ctx.Expect(rec.Code).ToEqual(r.code)
				ctx.Expect(refusal(ctx, rec)).ToEqual(r.err.Error())
			})

		s.It("hides an unexpected error behind a 500", func(ctx *specs.Context) {
			rec := do(mcpworker.Handler(&submitter{err: fmt.Errorf("sqlite: disk I/O at /var/x")}), http.MethodPost, "/worker/jobs/j-1/result", "Bearer tok", `{}`)
			ctx.Expect(rec.Code).ToEqual(http.StatusInternalServerError)
			ctx.Expect(refusal(ctx, rec)).ToEqual("internal error")
		})

		s.It("refuses a result over the size limit without submitting it", func(ctx *specs.Context) {
			sub := &submitter{}
			rec := do(mcpworker.Handler(sub), http.MethodPost, "/worker/jobs/j-1/result", "Bearer tok", strings.Repeat("x", mcpworker.MaxResultBytes+1))
			ctx.Expect(rec.Code).ToEqual(http.StatusRequestEntityTooLarge)
			ctx.Expect(sub.calls).To(specs.BeEmpty())
		})

		s.When("a builder runs the job's checks", func(s *specs.Spec) {
			s.It("hands the bearer token and the job to the submitter and answers the run, whether it passes and its report", func(ctx *specs.Context) {
				sub := &submitter{run: gateRun}
				rec := do(mcpworker.Handler(sub), http.MethodPost, "/worker/jobs/j-1/gate", "Bearer tok", "")
				ctx.Expect(rec.Code).ToEqual(http.StatusOK)
				ctx.Expect(sub.gates).ToEqual([]call{{token: "tok", job: "j-1"}})
				var g mcpworker.GateRun
				ctx.Expect(json.Unmarshal(rec.Body.Bytes(), &g)).To(specs.BeNil())
				ctx.Expect(g).ToEqual(mcpworker.GateRun{Job: "j-1", Passed: false, Run: gateRun, Report: gateReport})
			})

			s.It("never reads the body, so nothing in it reaches the checks", func(ctx *specs.Context) {
				sub := &submitter{run: gateRun}
				rec := do(mcpworker.Handler(sub), http.MethodPost, "/worker/jobs/j-1/gate", "Bearer tok", `{"command":["sh","-c","exit 0"],"skip":["test count"]}`)
				ctx.Expect(rec.Code).ToEqual(http.StatusOK)
				ctx.Expect(sub.gates).ToEqual([]call{{token: "tok", job: "j-1"}})
			})

			s.It("answers a role that may not run the checks with 403 and the error", func(ctx *specs.Context) {
				err := fmt.Errorf("worker: run gate: %w: role %q may not use tool %q", jobtoken.ErrForbidden, "reviewer-code", "run_gate")
				rec := do(mcpworker.Handler(&submitter{err: err}), http.MethodPost, "/worker/jobs/j-1/gate", "Bearer tok", "")
				ctx.Expect(rec.Code).ToEqual(http.StatusForbidden)
				ctx.Expect(refusal(ctx, rec)).ToEqual(err.Error())
			})

			s.It("hides a run that could not be made behind a 500", func(ctx *specs.Context) {
				rec := do(mcpworker.Handler(&submitter{err: fmt.Errorf("gate: checks of %q: exec: make: not found", "j-1")}), http.MethodPost, "/worker/jobs/j-1/gate", "Bearer tok", "")
				ctx.Expect(rec.Code).ToEqual(http.StatusInternalServerError)
				ctx.Expect(refusal(ctx, rec)).ToEqual("internal error")
			})
		})

		s.When("an agent asks a person a question", func(s *specs.Spec) {
			const question = `{"question":"Dark mode for everyone?","options":["yes","beta only"],"to":"product"}`

			s.It("hands the bearer token, the job and the body to the submitter and names the saved events", func(ctx *specs.Context) {
				sub := &submitter{}
				rec := do(mcpworker.Handler(sub), http.MethodPost, "/worker/jobs/j-1/ask", "Bearer tok", question)
				ctx.Expect(rec.Code).ToEqual(http.StatusOK)
				ctx.Expect(sub.asks).ToEqual([]call{{"tok", "j-1", question}})
				var a mcpworker.Accepted
				ctx.Expect(json.Unmarshal(rec.Body.Bytes(), &a)).To(specs.BeNil())
				ctx.Expect(a).ToEqual(mcpworker.Accepted{Job: "j-1", Events: []string{"HumanAsked", "NeedsInfo"}})
			})

			s.It("answers a role that may not ask with 403 and the error", func(ctx *specs.Context) {
				err := fmt.Errorf("worker: ask human: %w: role %q may not use tool %q", jobtoken.ErrForbidden, "builder", "ask_human")
				rec := do(mcpworker.Handler(&submitter{err: err}), http.MethodPost, "/worker/jobs/j-1/ask", "Bearer tok", question)
				ctx.Expect(rec.Code).ToEqual(http.StatusForbidden)
				ctx.Expect(refusal(ctx, rec)).ToEqual(err.Error())
			})

			s.It("refuses a question over the size limit without handing it over", func(ctx *specs.Context) {
				sub := &submitter{}
				rec := do(mcpworker.Handler(sub), http.MethodPost, "/worker/jobs/j-1/ask", "Bearer tok", strings.Repeat("x", mcpworker.MaxResultBytes+1))
				ctx.Expect(rec.Code).ToEqual(http.StatusRequestEntityTooLarge)
				ctx.Expect(sub.asks).To(specs.BeEmpty())
			})
		})

		s.It("refuses every other operation with a job token, without submitting anything", func(ctx *specs.Context) {
			sub := &submitter{}
			h := mcpworker.Handler(sub)
			for _, r := range []struct{ method, path string }{
				{http.MethodGet, "/worker/jobs/j-1/result"},
				{http.MethodPost, "/worker/jobs/j-1/approve"},
				{http.MethodPost, "/ops/job.approve"},
				{http.MethodGet, "/jobs"},
				{http.MethodPost, "/worker/jobs/j-1/result/extra"},
				{http.MethodGet, "/worker/jobs/j-1/gate"},
				{http.MethodGet, "/worker/jobs/j-1/ask"},
			} {
				rec := do(h, r.method, r.path, "Bearer tok", `{}`)
				ctx.Expect(rec.Code).ToEqual(http.StatusForbidden)
				ctx.Expect(refusal(ctx, rec)).To(specs.Contain("only takes a job's result"))
			}
			ctx.Expect(sub.calls).To(specs.BeEmpty())
			ctx.Expect(sub.gates).To(specs.BeEmpty())
			ctx.Expect(sub.asks).To(specs.BeEmpty())
		})
	})
}
