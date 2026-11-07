// Package worker is the slice of the calls an agent makes while it works on
// a job. Submit takes a role's result as JSON, with the session's job token,
// and turns it into the job's ResultSubmitted, the same for the MCP tool
// submit_result and for `factory worker submit-result` (01-diseno.md).
// Progress records what the agent says it is doing, for the MCP tool
// progress, as an event of the job.
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// Submitter takes results from agents. It is the single delivery point for
// every way an agent hands in a result: the MCP submit_result tool and the
// `factory worker submit-result` CLI both end here. The one-use guarantee of
// job tokens holds only because of that, so a new way to submit results must
// call Submit and never decide a ResultSubmitted on its own.
type Submitter struct {
	Tokens    tokens
	Jobs      jobs
	Pipelines pipelines
	Clock     ports.Clock

	// mu runs one submission at a time, so a token is revoked after its
	// result is saved before another submission can check it.
	mu sync.Mutex
	// outcomes keeps, by the hash of a valid token, what its submissions
	// came to since Take last read them, so an attempt knows whether its
	// session's turn handed in a result.
	outcomes map[[sha256.Size]byte]Outcome
}

// Outcome is what a token's submissions came to: Saved once one result was
// saved, else Refused, the error the last one was refused with, or nil when
// none was made.
type Outcome struct {
	Saved   bool
	Refused error
}

// Take returns token's Outcome since the last Take and forgets it.
func (s *Submitter) Take(token string) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(token))
	o := s.outcomes[key]
	delete(s.outcomes, key)
	return o
}

// record keeps err as token's last refusal, or that its result was saved
// when err is nil. Only tokens Authorize took are kept, so a caller with no
// valid token adds nothing; a saved one is revoked, so nothing follows it.
func (s *Submitter) record(token string, err error) {
	if s.outcomes == nil {
		s.outcomes = map[[sha256.Size]byte]Outcome{}
	}
	s.outcomes[sha256.Sum256([]byte(token))] = Outcome{Saved: err == nil, Refused: err}
}

// Submit decodes data as a result of the kind token was issued for, checks
// it and decides it on job as a ResultSubmitted from token's role. It
// returns the events the job saved and revokes token, so a token submits
// one result. A token not for job, for no submission, or from a round or
// stage entry the job has left is a jobtoken error; a result that does not
// decode or validate wraps domain.ErrMissingField or domain.ErrInvalidValue,
// naming what is wrong; one the job's stage does not take is Decide's error.
// In every case nothing is saved, and once the token is authorized its
// Outcome keeps the error for Take.
func (s *Submitter) Submit(ctx context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.Tokens.Authorize(token, jobtoken.OpSubmitResult, job)
	if err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
	}
	evs, err := s.submit(ctx, g, job, data)
	s.record(token, err)
	if err != nil {
		return nil, err
	}
	s.Tokens.Revoke(token)
	return evs, nil
}

func (s *Submitter) submit(ctx context.Context, g jobtoken.Grant, job domain.JobID, data []byte) ([]domain.Event, error) {
	j, v, err := s.Jobs.Load(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
	}
	hist, err := s.Jobs.History(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
	}
	if err := g.Current(j, hist); err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
	}
	r, err := decode(g.Kind, data)
	if err != nil {
		return nil, fmt.Errorf("worker: %s result: %w", g.Kind, err)
	}
	p, err := s.Pipelines.Pipeline(ctx, j.Pipeline)
	if err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
	}
	ev := domain.ResultSubmitted{Job: job, Role: g.Role, Result: r, At: s.Clock.Now()}
	evs, _, err := s.Jobs.Apply(ctx, job, v, p, ev)
	if err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
	}
	return evs, nil
}

func decode(k domain.Kind, data []byte) (domain.Result, error) {
	switch k {
	case domain.KindIntake:
		return decodeAs[domain.IntakeResult](data)
	case domain.KindSpec:
		return decodeAs[domain.SpecResult](data)
	case domain.KindBuild:
		return decodeAs[domain.BuildResult](data)
	case domain.KindReview:
		return decodeAs[domain.ReviewResult](data)
	case domain.KindApprove:
		return decodeAs[domain.ApproveResult](data)
	case domain.KindTask:
		return decodeAs[domain.TaskResult](data)
	case domain.KindTriage:
		return decodeAs[domain.TriageResult](data)
	}
	return nil, fmt.Errorf("%w: kind %q", domain.ErrInvalidValue, k)
}

// decodeAs reads data strictly, so a misspelt field is an error the agent
// sees instead of a field silently left empty.
func decodeAs[T domain.Result](data []byte) (domain.Result, error) {
	var r T
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%w: not a JSON object of this kind: %w", domain.ErrInvalidValue, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: more than one JSON value", domain.ErrInvalidValue)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}
