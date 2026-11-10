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
// once a result, or a question to a person, was saved with token, the
// session's job token: a question needs no result, since the job waits for
// the answer and the stage runs again with it. When the turn
// ends without one, it prompts s once more with Retry and the reason, so the
// same token submits it: a refused result does not spend it. When that turn
// misses too, it revokes token and records a domain.AttemptFailed from role
// on job with the reason and the last lines s wrote on stderr, and reports
// false; no third prompt is sent. A result saved with token before it was
// revoked still counts. A session that ended counts as a turn without a
// result.
func (a *Attempts) Run(ctx context.Context, s session, job domain.JobID, role, token, prompt string) (bool, error) {
	missed, err := a.turn(ctx, s, token, prompt)
	if err != nil || missed == "" {
		return err == nil, err
	}
	missed, err = a.turn(ctx, s, token, Retry(missed))
	if err != nil || missed == "" {
		return err == nil, err
	}
	return a.failSession(ctx, job, role, token, missed, s.Stderr(), nil)
}

// Retry is the prompt that asks an agent once more for its result, saying
// why factoryd has none. It names only the session's MCP tool: the session
// gets no job token in its environment, so the CLI could not hand one in.
func Retry(reason string) string {
	return "factoryd has no result from your last turn: " + reason + ".\n\n" +
		"Hand in your result now with the submit_result tool of the " + MCPServerName + " MCP server. " +
		"If a result was refused, fix what the error names. " +
		"You are asked only this once more: without a valid result now, the attempt fails.\n"
}

// turn runs one turn of s with msg and returns why no result was saved with
// token during it, or "" when one was or a question was asked.
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
	// A turn cut by ctx is not a missed turn, even when the session's end
	// reached ch first.
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("worker: attempt: %w", err)
	}
	o := a.Results.Take(token)
	switch {
	case o.Saved, o.Asked:
		return "", nil
	case o.Refused != nil:
		return "your result was refused: " + o.Refused.Error(), nil
	case ended != "":
		return "no result submitted, and the turn ended with: " + ended, nil
	}
	return "no result submitted", nil
}

// failSession revokes token, the session's token, and once every submission
// it made is in records role's attempt at job as failed for reason, unless
// one of them saved a result or asked a question. It reports whether one
// did.
func (a *Attempts) failSession(ctx context.Context, job domain.JobID, role, token, reason string, stderr []string, current func(domain.Job, []ports.Record) bool) (bool, error) {
	handed := false
	err := a.Results.Settle(job, token, func(o Outcome) error {
		if o.Saved || o.Asked {
			handed = true
			return nil
		}
		return a.fail(ctx, job, role, reason, stderr, current)
	})
	return handed, err
}

// fail records role's attempt at job as failed for reason, on the job as
// loaded, so a decision saved meanwhile makes it fail on the version. With
// current set, nothing is recorded unless current says the job, as loaded,
// is still where the attempt ran.
func (a *Attempts) fail(ctx context.Context, job domain.JobID, role, reason string, stderr []string, current func(domain.Job, []ports.Record) bool) error {
	ev := domain.AttemptFailed{Job: job, Role: role, Reason: reason, Stderr: stderr, At: a.Clock.Now()}
	if err := a.record(ctx, job, ev, current); err != nil {
		return fmt.Errorf("worker: failed attempt: %w", err)
	}
	return nil
}

// record decides ev on job as loaded. With current set, nothing is recorded
// unless current says the job, as loaded, is still where ev happened.
func (a *Attempts) record(ctx context.Context, job domain.JobID, ev domain.Event, current func(domain.Job, []ports.Record) bool) error {
	j, v, err := a.Jobs.Load(ctx, job)
	if err != nil {
		return err
	}
	if current != nil {
		hist, err := a.Jobs.History(ctx, job)
		if err != nil {
			return err
		}
		if !current(j, hist) {
			return nil
		}
	}
	p, err := a.Pipelines.Pipeline(ctx, j.Pipeline)
	if err != nil {
		return err
	}
	_, _, err = a.Jobs.Apply(ctx, job, v, p, ev)
	return err
}
