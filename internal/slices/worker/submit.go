// Package worker is the slice of the calls an agent makes while it works on
// a job. Submit takes a role's result as JSON, with the session's job token,
// and turns it into the job's ResultSubmitted, the same for the MCP tool
// submit_result and for `factory worker submit-result` (01-diseno.md).
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// Submitter takes results from agents.
type Submitter struct {
	Tokens    tokens
	Jobs      jobs
	Pipelines pipelines
	Clock     ports.Clock
}

// Submit decodes data as a result of the kind token was issued for, checks
// it and decides it on job as a ResultSubmitted from token's role. It
// returns the events the job saved. A token not for job, or for no
// submission, is a jobtoken error; a result that does not decode or validate
// wraps domain.ErrMissingField or domain.ErrInvalidValue, naming what is
// wrong; one the job's stage does not take is Decide's error. In every case
// nothing is saved.
func (s *Submitter) Submit(ctx context.Context, token string, job domain.JobID, data []byte) ([]domain.Event, error) {
	g, err := s.Tokens.Authorize(token, jobtoken.OpSubmitResult, job)
	if err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
	}
	r, err := decode(g.Kind, data)
	if err != nil {
		return nil, fmt.Errorf("worker: %s result: %w", g.Kind, err)
	}
	j, v, err := s.Jobs.Load(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("worker: submit result: %w", err)
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
