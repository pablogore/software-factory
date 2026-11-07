// Package mcpworker serves the worker surface of factoryd to the agents that
// work on jobs, with the session's job token. For now it is the HTTP route
// `factory worker submit-result` calls; it serves nothing else, so a job
// token gets no other operation from it (06-interfaz-y-observabilidad.md).
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

// Handler serves POST ResultPath with the token as a bearer token. Every
// other request is refused with 403.
func Handler(s Submitter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ResultPath, func(w http.ResponseWriter, r *http.Request) {
		submit(w, r, s)
	})
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
	evs, err := s.Submit(r.Context(), bearer(r), job, data)
	if err != nil {
		code := status(err)
		msg := err.Error()
		if code == http.StatusInternalServerError {
			msg = "internal error"
		}
		write(w, code, Refused{Error: msg})
		return
	}
	names := make([]string, len(evs))
	for i, ev := range evs {
		names[i] = ev.EventName()
	}
	write(w, http.StatusOK, Accepted{Job: job, Events: names})
}

// bearer is the request's bearer token, or "" without one.
func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
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
	case errors.Is(err, decide.ErrIllegalTransition), errors.Is(err, decide.ErrUnsupported),
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
