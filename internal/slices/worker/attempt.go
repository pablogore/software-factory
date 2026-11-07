package worker

import (
	"context"
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// Attempts runs a role's attempt at a job in an agent session and makes sure
// it hands in a result: an agent that ends its turn without one, or with one
// that was refused, is asked once more with why, and a second miss is a
// failed attempt (01-diseno.md).
type Attempts struct {
	Results   results
	Jobs      jobs
	Pipelines pipelines
	Clock     ports.Clock
}

// Run prompts s with prompt and waits for its turn to end. It reports true
// once a result was saved with token, the session's job token. When the turn
// ends without one, it prompts s once more with Retry and the reason, so the
// same token submits it: a refused result does not spend it. When that turn
// misses too, it records a domain.AttemptFailed from role on job with the
// reason and reports false; no third prompt is sent. A session that ended
// counts as a turn without a result.
func (a *Attempts) Run(ctx context.Context, s session, job domain.JobID, role, token, prompt string) (bool, error) {
	missed, err := a.turn(ctx, s, token, prompt)
	if err != nil || missed == "" {
		return err == nil, err
	}
	missed, err = a.turn(ctx, s, token, Retry(missed))
	if err != nil || missed == "" {
		return err == nil, err
	}
	return false, a.fail(ctx, job, role, missed)
}

// Retry is the prompt that asks an agent once more for its result, saying
// why factoryd has none.
func Retry(reason string) string {
	return "factoryd has no result from your last turn: " + reason + ".\n\n" +
		"Hand in your result now with submit_result: the MCP tool, or `factory worker submit-result` " +
		"with the JSON on stdin. If a result was refused, fix what the error names. " +
		"You are asked only this once more: without a valid result now, the attempt fails.\n"
}

// turn runs one turn of s with msg and returns why no result was saved with
// token during it, or "" when one was.
func (a *Attempts) turn(ctx context.Context, s session, token, msg string) (string, error) {
	ended := ""
	ch, err := s.Prompt(ctx, msg)
	if err != nil {
		ended = err.Error()
	}
	for done := ch == nil; !done; {
		select {
		case ev, ok := <-ch:
			done = !ok
			if ok && ev.Kind == domain.ProgressEnded && ev.Error != "" {
				ended = ev.Error
			}
		case <-ctx.Done():
			return "", fmt.Errorf("worker: attempt: %w", ctx.Err())
		}
	}
	o := a.Results.Take(token)
	switch {
	case o.Saved:
		return "", nil
	case o.Refused != nil:
		return "your result was refused: " + o.Refused.Error(), nil
	case ended != "":
		return "no result submitted, and the turn ended with: " + ended, nil
	}
	return "no result submitted", nil
}

func (a *Attempts) fail(ctx context.Context, job domain.JobID, role, reason string) error {
	j, v, err := a.Jobs.Load(ctx, job)
	if err != nil {
		return fmt.Errorf("worker: failed attempt: %w", err)
	}
	p, err := a.Pipelines.Pipeline(ctx, j.Pipeline)
	if err != nil {
		return fmt.Errorf("worker: failed attempt: %w", err)
	}
	ev := domain.AttemptFailed{Job: job, Role: role, Reason: reason, At: a.Clock.Now()}
	if _, _, err := a.Jobs.Apply(ctx, job, v, p, ev); err != nil {
		return fmt.Errorf("worker: failed attempt: %w", err)
	}
	return nil
}
