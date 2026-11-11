// Package mcpworker serves the worker surface of factoryd to the agents that
// work on jobs, with the session's job token. For now it is the HTTP routes
// `factory worker submit-result`, `factory worker run-gate` and `factory
// worker dev-server` and `factory worker ask-human` call and the MCP tools
// submit_result, progress, run_gate, dev_server, ask_human and
// context_pack, all over one Submitter; it
// serves nothing else, so a job token gets no other operation from it
// (06-interfaz-y-observabilidad.md).
package mcpworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/portrange"
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

// DevServerPath is the route a session's dev server is started by, with
// POST, and stopped by, with DELETE; {job} is its ID. Its body is never
// read: the command is the repo's and the port the job's.
const DevServerPath = "/worker/jobs/{job}/dev-server"

// AskPath is the route a question to a person is posted to; {job} is its
// ID. Its body is the question's JSON object, as the MCP tool ask_human's
// arguments are, and the Submitter decodes both the same way.
const AskPath = "/worker/jobs/{job}/ask"

// Submitter decides a result, records a progress message, runs the checks,
// records a question to a person or gives the context pack, on a job with
// the token it came with.
type Submitter interface {
	Submit(ctx context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error)
	Progress(ctx context.Context, token string, job domain.JobID, text string) ([]domain.Event, error)
	RunGate(ctx context.Context, token string, job domain.JobID) (domain.ChecksRun, error)
	DevServer(ctx context.Context, token string, job domain.JobID) (int, error)
	StopDevServer(ctx context.Context, token string, job domain.JobID) error
	AskHuman(ctx context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error)
	ContextPack(ctx context.Context, token string, job domain.JobID) (string, error)
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

// DevServer is the answer to a dev server call: the port it runs on, none
// once stopped, and Report, the answer as the agent reads it, worded here
// once so the MCP tool and the CLI show the same.
type DevServer struct {
	Job    domain.JobID `json:"job"`
	Port   int          `json:"port,omitempty"`
	Report string       `json:"report"`
}

// Refused is the answer to anything else.
type Refused struct {
	Error string `json:"error"`
}

// Handler serves POST ResultPath, POST GatePath, POST and DELETE
// DevServerPath, POST AskPath and the MCP server at MCPPath, all with the
// token as a bearer token and all over s, so a result is decided, the
// checks are run, the dev server is run and a question is recorded the same
// way whichever one the call comes by. Every other request is refused with 403.
func Handler(s Submitter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ResultPath, func(w http.ResponseWriter, r *http.Request) {
		submit(w, r, s)
	})
	mux.HandleFunc("POST "+GatePath, func(w http.ResponseWriter, r *http.Request) {
		runGate(w, r, s)
	})
	mux.HandleFunc("POST "+DevServerPath, func(w http.ResponseWriter, r *http.Request) {
		devServer(w, r, s, false)
	})
	mux.HandleFunc("DELETE "+DevServerPath, func(w http.ResponseWriter, r *http.Request) {
		devServer(w, r, s, true)
	})
	mux.HandleFunc("POST "+AskPath, func(w http.ResponseWriter, r *http.Request) {
		askHuman(w, r, s)
	})
	mux.Handle(MCPPath, mcpHandler(s))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusForbidden, Refused{Error: jobtoken.ErrForbidden.Error() + ": the worker API only takes a job's result, runs its checks and its dev server and takes its questions to a person"})
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

func devServer(w http.ResponseWriter, r *http.Request, s Submitter, stop bool) {
	d, err := callDevServer(r.Context(), s, bearer(r.Header), domain.JobID(r.PathValue("job")), stop)
	if err != nil {
		code, msg := refusal(err)
		write(w, code, Refused{Error: msg})
		return
	}
	write(w, http.StatusOK, d)
}

// callDevServer starts job's dev server for token, or stops it, and words
// the answer, for the route and the MCP tool alike.
func callDevServer(ctx context.Context, s Submitter, token string, job domain.JobID, stop bool) (DevServer, error) {
	if stop {
		if err := s.StopDevServer(ctx, token, job); err != nil {
			return DevServer{}, err
		}
		return DevServer{Job: job, Report: fmt.Sprintf("dev server of job %s stopped\n", job)}, nil
	}
	port, err := s.DevServer(ctx, token, job)
	if err != nil {
		return DevServer{}, err
	}
	return DevServer{Job: job, Port: port, Report: fmt.Sprintf("dev server of job %s on port %d: http://localhost:%d\n", job, port, port)}, nil
}

func askHuman(w http.ResponseWriter, r *http.Request, s Submitter) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxResultBytes))
	if err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			write(w, http.StatusRequestEntityTooLarge, Refused{Error: "question over 1 MiB"})
			return
		}
		write(w, http.StatusBadRequest, Refused{Error: "cannot read the question"})
		return
	}
	job := domain.JobID(r.PathValue("job"))
	evs, err := s.AskHuman(r.Context(), bearer(r.Header), job, data)
	if err != nil {
		code, msg := refusal(err)
		write(w, code, Refused{Error: msg})
		return
	}
	write(w, http.StatusOK, accepted(job, evs))
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
		errors.Is(err, gatelock.ErrPreempted), errors.Is(err, portrange.ErrNoFreePort), errors.Is(err, portrange.ErrNoFreeRange):
		return http.StatusConflict
	case errors.Is(err, domain.ErrMissingField), errors.Is(err, domain.ErrInvalidValue):
		return http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrTimedOut):
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

func write(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
