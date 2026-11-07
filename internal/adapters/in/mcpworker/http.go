// Package mcpworker serves the worker surface of factoryd to the agents that
// work on jobs, with the session's job token. For now it is the HTTP route
// `factory worker submit-result` calls and the MCP tool submit_result, both
// over one Submitter; it serves nothing else, so a job token gets no other
// operation from it (06-interfaz-y-observabilidad.md).
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
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// MaxResultBytes is the largest result body the route reads.
const MaxResultBytes = 1 << 20

// ResultPath is the route a job's result is posted to; {job} is its ID.
const ResultPath = "/worker/jobs/{job}/result"

// Submitter decides a result on a job with the token it came with.
type Submitter interface {
	Submit(ctx context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error)
}

// Accepted is the answer to a result that was decided: the job and the
// names of the events it saved.
type Accepted struct {
	Job    domain.JobID `json:"job"`
	Events []string     `json:"events"`
}

// Refused is the answer to anything else.
type Refused struct {
	Error string `json:"error"`
}

// Handler serves POST ResultPath and the MCP server at MCPPath, both with
// the token as a bearer token and both submitting to s, so a result is
// decided the same way whichever one it comes by. Every other request is
// refused with 403.
func Handler(s Submitter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ResultPath, func(w http.ResponseWriter, r *http.Request) {
		submit(w, r, s)
	})
	mux.Handle(MCPPath, mcpHandler(s))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusForbidden, Refused{Error: jobtoken.ErrForbidden.Error() + ": the worker API only takes a job's result"})
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
		errors.Is(err, decide.ErrStopped), errors.Is(err, ports.ErrVersionConflict):
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
