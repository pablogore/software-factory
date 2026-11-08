// Package mcpworker serves the worker surface of factoryd to the agents that
// work on jobs, with the session's job token. For now it is the HTTP routes
// `factory worker submit-result` and `factory worker run-gate` call and the
// MCP tools submit_result, progress and run_gate, all over one Submitter; it
// serves nothing else, so a job token gets no other operation from it
// (06-interfaz-y-observabilidad.md).
package mcpworker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// MaxResultBytes is the largest result body the route reads, and the
// largest arguments the MCP tools take.
const MaxResultBytes = 1 << 20

// ResultPath is the route a job's result is posted to; {job} is its ID.
const ResultPath = "/worker/jobs/{job}/result"

// GatePath is the route a job's checks are run by; {job} is its ID. Its
// body is never read: nothing but the token and the job reaches the checks.
const GatePath = "/worker/jobs/{job}/gate"

// Submitter decides a result, records a progress message or runs the
// checks, on a job with the token it came with.
type Submitter interface {
	Submit(ctx context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error)
	Progress(ctx context.Context, token string, job domain.JobID, text string) ([]domain.Event, error)
	RunGate(ctx context.Context, token string, job domain.JobID) (domain.ChecksRun, error)
}

// Accepted is the answer to a result that was decided: the job and the
// names of the events it saved.
type Accepted struct {
	Job    domain.JobID `json:"job"`
	Events []string     `json:"events"`
}

// GateRun is the answer to a run of a job's checks: whether they pass, as
// the gate would judge them, the run itself and Report, the run as the agent
// reads it, worded here once so the MCP tool and the CLI show the same.
type GateRun struct {
	Job    domain.JobID     `json:"job"`
	Passed bool             `json:"passed"`
	Run    domain.ChecksRun `json:"run"`
	Report string           `json:"report"`
}

// Refused is the answer to anything else.
type Refused struct {
	Error string `json:"error"`
}

// Handler serves POST ResultPath, POST GatePath and the MCP server at
// MCPPath, all with the token as a bearer token and all over s, so a result
// is decided, and the checks are run, the same way whichever one the call
// comes by. Every other request is refused with 403.
func Handler(s Submitter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ResultPath, func(w http.ResponseWriter, r *http.Request) {
		submit(w, r, s)
	})
	mux.HandleFunc("POST "+GatePath, func(w http.ResponseWriter, r *http.Request) {
		runGate(w, r, s)
	})
	mux.Handle(MCPPath, mcpHandler(s))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusForbidden, Refused{Error: jobtoken.ErrForbidden.Error() + ": the worker API only takes a job's result and runs its checks"})
	})
	return mux
}

func submit(w http.ResponseWriter, r *http.Request, s Submitter) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxResultBytes))
	if err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			write(w, http.StatusRequestEntityTooLarge, Refused{Error: "result over 1 MiB"})
			return
		}
		write(w, http.StatusBadRequest, Refused{Error: "cannot read the result"})
		return
	}
	job := domain.JobID(r.PathValue("job"))
	evs, err := s.Submit(r.Context(), bearer(r.Header), job, data)
	if err != nil {
		code, msg := refusal(err)
		write(w, code, Refused{Error: msg})
		return
	}
	write(w, http.StatusOK, accepted(job, evs))
}

func runGate(w http.ResponseWriter, r *http.Request, s Submitter) {
	job := domain.JobID(r.PathValue("job"))
	run, err := s.RunGate(r.Context(), bearer(r.Header), job)
	if err != nil {
		code, msg := refusal(err)
		write(w, code, Refused{Error: msg})
		return
	}
	write(w, http.StatusOK, gateRun(job, run))
}

func accepted(job domain.JobID, evs []domain.Event) Accepted {
	names := make([]string, len(evs))
	for i, ev := range evs {
		names[i] = ev.EventName()
	}
	return Accepted{Job: job, Events: names}
}

// refusal is the status and message a submission's err is answered with;
// an unexpected error is hidden behind "internal error".
func refusal(err error) (int, string) {
	code := status(err)
	if code == http.StatusInternalServerError {
		return code, "internal error"
	}
	return code, err.Error()
}

// bearer is the bearer token in h, or "" without one.
func bearer(h http.Header) string {
	token, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

func status(err error) int {
	switch {
	case errors.Is(err, jobtoken.ErrNoToken), errors.Is(err, jobtoken.ErrUnknownToken):
		return http.StatusUnauthorized
	case errors.Is(err, jobtoken.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ports.ErrJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, jobtoken.ErrStale), errors.Is(err, decide.ErrIllegalTransition), errors.Is(err, decide.ErrUnsupported),
		errors.Is(err, decide.ErrStopped), errors.Is(err, ports.ErrVersionConflict), errors.Is(err, domain.ErrBusy),
		errors.Is(err, gatelock.ErrPreempted):
		return http.StatusConflict
	case errors.Is(err, domain.ErrMissingField), errors.Is(err, domain.ErrInvalidValue):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

func write(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
