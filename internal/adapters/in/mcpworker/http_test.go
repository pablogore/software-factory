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
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// submitter records its calls and answers with err, or with one
// StageEntered.
type submitter struct {
	err   error
	calls []call
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

		s.It("refuses every other operation with a job token, without submitting anything", func(ctx *specs.Context) {
			sub := &submitter{}
			h := mcpworker.Handler(sub)
			for _, r := range []struct{ method, path string }{
				{http.MethodGet, "/worker/jobs/j-1/result"},
				{http.MethodPost, "/worker/jobs/j-1/approve"},
				{http.MethodPost, "/ops/job.approve"},
				{http.MethodGet, "/jobs"},
				{http.MethodPost, "/worker/jobs/j-1/result/extra"},
			} {
				rec := do(h, r.method, r.path, "Bearer tok", `{}`)
				ctx.Expect(rec.Code).ToEqual(http.StatusForbidden)
				ctx.Expect(refusal(ctx, rec)).To(specs.Contain("only takes a job's result"))
			}
			ctx.Expect(sub.calls).To(specs.BeEmpty())
		})
	})
}
