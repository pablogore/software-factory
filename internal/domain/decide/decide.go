package decide

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// ErrIllegalTransition means the event does not fit the job's current stage.
// Decide returns no events with it, so nothing changes.
var ErrIllegalTransition = errors.New("illegal transition")

// ErrUnsupported means the result or message is legal in this stage but its
// outcome is not decided yet. Among agent results it covers only task results
// (#76); otherwise only a person's message outside needs-human, needs-info
// and done, and a person's feedback on a job stopped for its budget.
var ErrUnsupported = errors.New("unsupported result")

// ErrOtherJob means the event is addressed to a different job than the one
// it was decided for. Decide returns no events with it, so nothing changes.
var ErrOtherJob = errors.New("event for another job")

// ErrStopped means a person stopped the job, so no event moves it any more.
// Decide returns no events with it, so nothing changes.
var ErrStopped = errors.New("job stopped")

// ErrOKRefused means a person's OK in needs-human cannot act on the job's
// cause: the work did not pass gate and review (builder BLOCKED, failed gate)
// or has no spec to pass (specgate escalation), so an OK never merges it and
// a person's feedback is the way out. The error names the cause. Decide
// returns no events with it, so the job stays in needs-human.
var ErrOKRefused = errors.New("OK refused")

// ErrOverrideRefused means a person's override in needs-human does not fit
// the job's cause: only work stopped by CHANGES in the last round may be
// merged over review, since every other cause either never passed gate and
// review or has its own way out through an OK. The error names the cause. Decide
// returns no events with it, so the job stays in needs-human.
var ErrOverrideRefused = errors.New("override refused")

// ErrStaleWait means a person's OK, override or message names a wait other
// than the job's current one, so it answers an earlier wait (a replay) and
// must not act on this one. The error names the expected WaitSeq, never the
// stage. Decide returns no events with it, so nothing changes.
var ErrStaleWait = errors.New("action for another wait")

// ErrReworkRefused means a person's feedback in needs-human would be one more
// rework than the job's human_reworks allows (a person's override for this
// job, else the pipeline's), so a job that keeps getting stuck ends with a
// final decision instead of looping. The error names
// the limit. Approve, reject (a direction) and stop still act on the job.
// Decide returns no events with it, so the job stays in needs-human.
var ErrReworkRefused = errors.New("rework refused")

// Decide returns the events that follow ev for job in pipeline p. It never
// reads a clock: every event it returns carries the time of ev.
func Decide(job domain.Job, ev domain.Event, p domain.Pipeline) ([]domain.Event, error) {
	if isNil(ev) {
		return nil, fmt.Errorf("%w: no event in stage %q", ErrIllegalTransition, job.Stage)
	}
	if ev.JobID() != job.ID {
		return nil, fmt.Errorf("%w: %s for job %q decided for job %q", ErrOtherJob, ev.EventName(), ev.JobID(), job.ID)
	}
	switch e := ev.(type) {
	case domain.MessageReceived:
		if ignored(e.Allowed, e.AuthorKind) {
			return nil, nil
		}
	case domain.OverrideRequested:
		if ignored(e.Allowed, e.AuthorKind) {
			return nil, nil
		}
	case domain.LimitOverrideRequested:
		if ignored(e.Allowed, e.AuthorKind) {
			return nil, nil
		}
	case domain.CapRaiseRequested:
		if ignored(e.Allowed, e.AuthorKind) {
			return nil, nil
		}
	case domain.ActionFailed:
		// A job that ended needs no person for it: the action stays listed as
		// failed, and delivery is not stopped by a refusal.
		if job.Stage == domain.StageStopped || job.Stage == domain.StageRejected {
			return nil, nil
		}
	}
	if job.Stage == domain.StageStopped {
		return nil, fmt.Errorf("%w: %s", ErrStopped, ev.EventName())
	}
	if job.Stage == domain.StageRejected {
		return nil, illegal(job, ev)
	}
	switch e := ev.(type) {
	case domain.StopRequested:
		if domain.Blank(e.By) {
			return nil, fmt.Errorf("decide: stop request without who asked: %w", domain.ErrMissingField)
		}
		return []domain.Event{domain.JobStopped{Job: job.ID, By: e.By, At: e.At}}, nil
	case domain.ResultSubmitted:
		return decideResult(job, e, p)
	case domain.SpecgatePassed:
		return afterGate(job, ev, domain.StageSpecgate, domain.KindSpec, e.At, p)
	case domain.GatePassed:
		return gatePassed(job, e, p)
	case domain.SpecgateFailed:
		return specgateFailed(job, e, p)
	case domain.GateFailed:
		return gateFailed(job, e, p)
	case domain.MergeFailed:
		return mergeFailed(job, e, p)
	case domain.BudgetExhausted:
		return budgetExhausted(job, e, p)
	case domain.HumanApproved:
		if domain.Blank(e.By) {
			return nil, fmt.Errorf("decide: approval without who gave it: %w", domain.ErrMissingField)
		}
		if err := fresh(job, ev, e.WaitSeq); err != nil {
			return nil, err
		}
		if job.Stage == domain.StageNeedsHuman {
			return approved(job, e, p)
		}
	case domain.OverrideRequested:
		// Checked after the stopped and rejected refusals, as for messages.
		if domain.Blank(e.By) {
			return nil, fmt.Errorf("decide: override without who asked: %w", domain.ErrMissingField)
		}
		if domain.Blank(e.Reason) {
			return nil, fmt.Errorf("decide: override without a reason: %w", domain.ErrMissingField)
		}
		if err := fresh(job, ev, e.WaitSeq); err != nil {
			return nil, err
		}
		if job.Stage == domain.StageNeedsHuman {
			return overridden(job, e)
		}
	case domain.LimitOverrideRequested:
		if domain.Blank(e.By) {
			return nil, fmt.Errorf("decide: limit override without who asked: %w", domain.ErrMissingField)
		}
		if err := fresh(job, ev, e.WaitSeq); err != nil {
			return nil, err
		}
		if job.Stage == domain.StageNeedsHuman {
			return limitOverridden(job, e)
		}
	case domain.CapRaiseRequested:
		return capRaised(job, e, p)
	case domain.ActionFailed:
		return actionFailed(job, e)
	case domain.AttemptFailed:
		return attemptFailed(e)
	case domain.MergeCompleted:
		if job.Stage == domain.StageMerge {
			evs := []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageDone, At: e.At}}
			if job.Initiative != "" {
				evs = append(evs, domain.ReplanRequested{Job: job.ID, Initiative: job.Initiative, At: e.At})
			}
			return evs, nil
		}
	case domain.MessageReceived:
		// Checked after the stopped and rejected refusals, so the missing
		// name never tells a person more about the job than any other event.
		if domain.Blank(e.Author) {
			return nil, fmt.Errorf("decide: message without who sent it: %w", domain.ErrMissingField)
		}
		// A direction that drops a slice outside a wait answers no wait: the
		// drop ends the job, so no later wait can replay it.
		if !drops(job, e) || waiting(job.Stage) {
			if err := fresh(job, ev, e.WaitSeq); err != nil {
				return nil, err
			}
		}
		return decideMessage(job, e, p)
	}
	return nil, illegal(job, ev)
}

// Apply folds events into job and returns the new job. It does not modify
// the job it receives.
func Apply(job domain.Job, evs []domain.Event) domain.Job {
	for _, ev := range evs {
		switch e := ev.(type) {
		case domain.StageEntered:
			job.Stage, job.InfoFrom, job.Passed, job.Changes = e.Stage, "", nil, nil
			job = leftNeedsHuman(job)
			if waiting(e.Stage) {
				job.WaitSeq++
			}
		case domain.NeedsInfo:
			job.Stage, job.InfoFrom, job.Passed, job.Changes = domain.StageNeedsInfo, e.From, nil, nil
			job = leftNeedsHuman(job)
			job.WaitSeq++
		case domain.ReviewPassed:
			job.Passed = append(slices.Clip(job.Passed), e.Role)
		case domain.ChangesRequested:
			job.Changes = append(slices.Clip(job.Changes), e.Role)
		case domain.SpecRejected:
			job.SpecRejections = e.Rejections
		case domain.CapRaised:
			job.Budget = e.Value
		case domain.LimitOverridden:
			job = overrideLimit(job, e)
			// It answered the wait, so a replay of it never acts again.
			job.WaitSeq++
		case domain.ReworkRequested:
			// Only a rework from needs-human back to build counts toward
			// human_reworks: a comment reopening a merged job in done does not,
			// nor does feedback back to spec, which spec_rejections bounds. The
			// cause is still recorded here, so old histories replay as they are.
			if job.Stage == domain.StageNeedsHuman && !specBound(job.Cause) {
				job.HumanReworks++
			}
		case domain.RoundStarted:
			job.Round, job.Reviewers, job.Passed, job.Changes = e.Round, e.Reviewers, nil, nil
		case domain.NeedsHuman:
			job.Paused = paused(job, e)
			job.Stage, job.InfoFrom, job.Reviewers, job.Passed, job.Changes = domain.StageNeedsHuman, "", nil, nil, nil
			job.Reason, job.Cause, job.Question, job.Wall = e.Reason, e.Cause, e.Question, ""
			job.WaitSeq++
		case domain.TriageRequested:
			// Reviewers stay, so a retried round runs the same ones again.
			job.Stage, job.InfoFrom, job.Passed, job.Changes = domain.StageTriage, "", nil, nil
			job = leftNeedsHuman(job)
			job.Reason, job.Wall = e.Reason, e.Cause
		case domain.TriageRetried:
			job.TriageRetries++
		case domain.MergeRequested:
			job.Stage, job.InfoFrom, job.Reviewers, job.Passed, job.Changes = domain.StageMerge, "", nil, nil, nil
			job = leftNeedsHuman(job)
		case domain.JobStopped:
			job.Stage, job.InfoFrom, job.Reviewers, job.Passed, job.Changes = domain.StageStopped, "", nil, nil, nil
			job = leftNeedsHuman(job)
		case domain.SliceDropped:
			job.Stage, job.InfoFrom, job.Reviewers, job.Passed, job.Changes = domain.StageRejected, "", nil, nil, nil
			job = leftNeedsHuman(job)
		case domain.JobRejected:
			job.Stage, job.InfoFrom, job.Reviewers, job.Passed, job.Changes = domain.StageRejected, "", nil, nil, nil
			job = leftNeedsHuman(job)
			job.Reason = e.Reason
		}
	}
	return job
}

// leftNeedsHuman forgets why the job was in needs-human or triage, so a later
// stop for a person never inherits an old cause or question.
func leftNeedsHuman(job domain.Job) domain.Job {
	job.Reason, job.Cause, job.Question, job.Wall, job.Paused = "", "", "", "", nil
	return job
}

// paused is where job was when e stopped it for its spent budget, read before
// e is applied; nil for every other cause, so no later wait inherits it.
func paused(job domain.Job, e domain.NeedsHuman) *domain.Paused {
	if e.Cause != domain.CauseBudget {
		return nil
	}
	return &domain.Paused{Stage: job.Stage, Reason: job.Reason, Wall: job.Wall, Spent: e.Spent}
}

func decideResult(job domain.Job, e domain.ResultSubmitted, p domain.Pipeline) ([]domain.Event, error) {
	if job.Stage == domain.StageTriage {
		return triaged(job, e, p)
	}
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Name == job.Stage })
	if i < 0 || isNil(e.Result) {
		return nil, illegal(job, e)
	}
	stage := p.Stages[i]
	if e.Result.Kind() != stage.Kind || (len(stage.Roles) > 0 && !slices.Contains(stage.Roles, e.Role)) {
		return nil, illegal(job, e)
	}
	if err := e.Result.Validate(); err != nil {
		return nil, fmt.Errorf("decide: %s result from %q: %w", stage.Kind, e.Role, err)
	}

	switch r := e.Result.(type) {
	case domain.IntakeResult:
		switch r.Decision {
		case domain.IntakeAccept:
			return next(job, p, i, e.At), nil
		case domain.IntakeNeedsInfo:
			return []domain.Event{domain.NeedsInfo{Job: job.ID, From: stage.Name, At: e.At}}, nil
		case domain.IntakeReject:
			evs := []domain.Event{domain.JobRejected{Job: job.ID, Reason: rejected(r.Reason), At: e.At}}
			// A rejected slice never merges, so like a dropped one it asks its
			// initiative for a replan instead of leaving it on a dead job.
			if job.Initiative != "" {
				evs = append(evs, domain.ReplanRequested{Job: job.ID, Initiative: job.Initiative, At: e.At})
			}
			return evs, nil
		}
	case domain.SpecResult:
		return []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageSpecgate, At: e.At}}, nil
	case domain.BuildResult:
		if r.Status == domain.BuildReady {
			return []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageGate, At: e.At}}, nil
		}
		return wall(job, p, domain.CauseBlocked, blocked(e.Role, r.Summary), e.At), nil
	case domain.ReviewResult:
		return reviewed(job, e, r, p, i)
	case domain.ApproveResult:
		if r.Decision == domain.ApproveApprove {
			return next(job, p, i, e.At), nil
		}
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: escalated(e.Role, r.Reasons), Cause: domain.CauseEscalated, At: e.At}}, nil
	}
	return nil, fmt.Errorf("%w: %s result from %q in stage %q", ErrUnsupported, stage.Kind, e.Role, job.Stage)
}

// ignored reports whether Decide drops a message or an override without
// error: the event source did not mark it as coming from an allowed person,
// so bots, CI and strangers change nothing, in every stage.
func ignored(allowed bool, kind domain.AuthorKind) bool {
	return !allowed || kind != domain.AuthorPerson
}

// waiting reports whether stage waits on a person: needs-human, needs-info
// or done, the stages a person's OK, override or message answers.
func waiting(stage string) bool {
	return stage == domain.StageNeedsHuman || stage == domain.StageNeedsInfo || stage == domain.StageDone
}

// fresh refuses a person's action unless seq names the job's current wait,
// so an OK, override or message given for an earlier wait, delivered again,
// never acts on a later one. It runs before the stage is looked at, so a
// stale or missing seq tells the sender nothing about the stage.
func fresh(job domain.Job, ev domain.Event, seq int) error {
	if seq == 0 {
		return fmt.Errorf("decide: %s without the wait it answers: %w", ev.EventName(), domain.ErrMissingField)
	}
	if seq != job.WaitSeq {
		return fmt.Errorf("%w: %s for wait %d, expected wait %d", ErrStaleWait, ev.EventName(), seq, job.WaitSeq)
	}
	return nil
}

// drops reports whether a person's message is a direction that drops the
// unmerged slice job.
func drops(job domain.Job, e domain.MessageReceived) bool {
	return e.Direction && job.Initiative != "" && job.Stage != domain.StageMerge && job.Stage != domain.StageDone
}

// approved decides a person's OK in needs-human from the job's recorded
// cause, never from Reason: only an approver ESCALATE, which
// comes after gate and review passed, merges. After CHANGES in the last round
// the job goes back to build in the same round, and on a length exception
// the spec passes specgate as it is. A missing or unknown cause fails closed.
func approved(job domain.Job, e domain.HumanApproved, p domain.Pipeline) ([]domain.Event, error) {
	if err := job.Cause.Validate(); err != nil {
		return nil, fmt.Errorf("decide: OK in needs-human: %w", err)
	}
	switch job.Cause {
	case domain.CauseEscalated:
		return []domain.Event{domain.MergeRequested{Job: job.ID, At: e.At}}, nil
	case domain.CauseChanges:
		i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindBuild })
		if i < 0 {
			return nil, fmt.Errorf("decide: pipeline %q has no build stage", p.Name)
		}
		return []domain.Event{domain.StageEntered{Job: job.ID, Stage: p.Stages[i].Name, At: e.At}}, nil
	case domain.CauseSpecException:
		return afterGate(job, e, domain.StageNeedsHuman, domain.KindSpec, e.At, p)
	}
	return nil, fmt.Errorf("%w: cause %q", ErrOKRefused, job.Cause)
}

// overridden decides a person's override in needs-human from the job's
// recorded cause, never from Reason or the override's own reason: only
// CHANGES in the last round merges, recording who and why first. A missing
// or unknown cause fails closed, and every other cause is refused.
func overridden(job domain.Job, e domain.OverrideRequested) ([]domain.Event, error) {
	if err := job.Cause.Validate(); err != nil {
		return nil, fmt.Errorf("decide: override in needs-human: %w", err)
	}
	if job.Cause != domain.CauseChanges {
		return nil, fmt.Errorf("%w: cause %q", ErrOverrideRefused, job.Cause)
	}
	return []domain.Event{
		domain.MergeOverridden{Job: job.ID, By: e.By, Reason: e.Reason, At: e.At},
		domain.MergeRequested{Job: job.ID, At: e.At},
	}, nil
}

// limitOverridden records a person's change of one limit for this job, once
// the name and value pass the same checks as the pipeline's limits block and
// the value is within the override's cap, so no override lets a job loop for
// long. It does not look at the cause: the job keeps waiting for the same reason.
func limitOverridden(job domain.Job, e domain.LimitOverrideRequested) ([]domain.Event, error) {
	n, unlimited, err := domain.ParseLimit(e.Limit, e.Value)
	if err != nil {
		return nil, fmt.Errorf("decide: limit override: %w", err)
	}
	if !unlimited && n > domain.OverrideCap(e.Limit) {
		return nil, fmt.Errorf("decide: limit override: %w: %s %q, want at most %d", domain.ErrInvalidValue, e.Limit, e.Value, domain.OverrideCap(e.Limit))
	}
	return []domain.Event{domain.LimitOverridden{Job: job.ID, By: e.By, Limit: e.Limit, Value: n, Unlimited: unlimited, At: e.At}}, nil
}

// overrideLimit sets the job's own value of the limit e names.
func overrideLimit(job domain.Job, e domain.LimitOverridden) domain.Job {
	n := e.Value
	switch e.Limit {
	case "spec_rejections":
		job.Limits.SpecRejections = &n
	case "human_reworks":
		job.Limits.HumanReworks, job.ReworksLifted = &n, false
		if e.Unlimited {
			job.Limits.HumanReworks, job.ReworksLifted = nil, true
		}
	case "triage_retries":
		job.Limits.TriageRetries = &n
	case "max_rounds":
		job.Limits.MaxRounds = &n
	}
	return job
}

// humanReworks is the job's own human_reworks when a person overrode it,
// else the pipeline's; false means unlimited.
func humanReworks(job domain.Job, p domain.Pipeline) (int, bool) {
	switch {
	case job.ReworksLifted:
		return 0, false
	case job.Limits.HumanReworks != nil:
		return *job.Limits.HumanReworks, true
	default:
		return p.HumanReworks()
	}
}

// maxRounds is the job's own max_rounds when a person overrode it, which wins
// even over the stage's own value, else the pipeline's for stage st.
func maxRounds(job domain.Job, p domain.Pipeline, st domain.Stage) int {
	if job.Limits.MaxRounds != nil {
		return *job.Limits.MaxRounds
	}
	return p.MaxRounds(st)
}

// roundLimit is how many rounds the job has in review stage st: its
// maxRounds plus one for every round triage gave it, so a retried job gets
// its round and a wall in it goes to triage again.
func roundLimit(job domain.Job, p domain.Pipeline, st domain.Stage) int {
	return maxRounds(job, p, st) + job.TriageRetries
}

// triageRetries is the job's own triage_retries when a person overrode it,
// else the pipeline's.
func triageRetries(job domain.Job, p domain.Pipeline) int {
	if job.Limits.TriageRetries != nil {
		return *job.Limits.TriageRetries
	}
	return p.TriageRetries()
}

// specRejections is the job's own spec_rejections when a person overrode it,
// else the pipeline's.
func specRejections(job domain.Job, p domain.Pipeline) int {
	if job.Limits.SpecRejections != nil {
		return *job.Limits.SpecRejections
	}
	return p.SpecRejections()
}

// decideMessage handles an allowed person's message. Marked as a direction on
// an unmerged slice, it drops the slice. In needs-info it is the
// answer, so the job goes back to the stage that asked. In needs-human it is
// feedback routed on the recorded cause (see feedback). In done it reopens the
// merged job: the job goes back to the build stage in the same round, with the
// text as input, so a reopened job past its last round still needs a person
// after CHANGES.
func decideMessage(job domain.Job, e domain.MessageReceived, p domain.Pipeline) ([]domain.Event, error) {
	if drops(job, e) {
		return dropped(job, e), nil
	}
	switch job.Stage {
	case domain.StageNeedsInfo:
		return answered(job, e, p)
	case domain.StageNeedsHuman:
		return feedback(job, e, p)
	case domain.StageDone:
		return reworked(job, e, p)
	}
	return nil, fmt.Errorf("%w: message from %q in stage %q", ErrUnsupported, e.Author, job.Stage)
}

// feedback decides a person's feedback in needs-human from the job's recorded
// cause, never from Reason or the text: after specgate's escalation or on a
// length exception the spec is rewritten with it (not read from the rejection
// count, which can be stale), after a spent budget it is refused and the job
// keeps waiting for a raise of its budget, and after every other cause the job goes back to build with
// it. A missing or unknown cause fails closed, and the switch
// refuses by default, so a future cause is never routed by accident. Once the
// job has used every rework its human_reworks allows (a person's override for
// this job, else the pipeline's), feedback back to build is refused; feedback
// back to spec is not, since spec_rejections already bounds that loop.
func feedback(job domain.Job, e domain.MessageReceived, p domain.Pipeline) ([]domain.Event, error) {
	if err := job.Cause.Validate(); err != nil {
		return nil, fmt.Errorf("decide: feedback in needs-human: %w", err)
	}
	switch job.Cause {
	case domain.CauseSpecEscalated:
		return respecified(job, e, p, true)
	case domain.CauseSpecException:
		return respecified(job, e, p, false)
	case domain.CauseBlocked, domain.CauseGateFailed, domain.CauseChanges, domain.CauseEscalated, domain.CauseActionFailed, domain.CauseMergeFailed:
		if limit, ok := humanReworks(job, p); ok && job.HumanReworks >= limit {
			return nil, fmt.Errorf("%w: %d of human_reworks %d used", ErrReworkRefused, job.HumanReworks, limit)
		}
		return reworked(job, e, p)
	case domain.CauseBudget:
		// Only a raise of its budget resumes it, so the job keeps waiting
		// rather than spending more.
		return nil, fmt.Errorf("%w: feedback in needs-human, cause %q", ErrUnsupported, job.Cause)
	}
	return nil, fmt.Errorf("decide: feedback in needs-human, cause %q: %w", job.Cause, domain.ErrInvalidValue)
}

// reworked sends the job back to the build stage in the same round with the
// person's text as input.
func reworked(job domain.Job, e domain.MessageReceived, p domain.Pipeline) ([]domain.Event, error) {
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindBuild })
	if i < 0 {
		return nil, fmt.Errorf("decide: pipeline %q has no build stage", p.Name)
	}
	return []domain.Event{
		domain.ReworkRequested{Job: job.ID, By: e.Author, Text: e.Text, At: e.At},
		domain.StageEntered{Job: job.ID, Stage: p.Stages[i].Name, At: e.At},
	}, nil
}

// specBound reports whether feedback on cause sends the job back to spec.
func specBound(cause domain.HumanCause) bool {
	return cause == domain.CauseSpecEscalated || cause == domain.CauseSpecException
}

// respecified sends a job back to the spec stage with the person's feedback
// as input. After specgate rejected it too often the rejection count starts
// again (reset), so the rewritten spec gets every specgate try a new spec
// gets; a length exception never counted as a rejection, so it keeps its count.
func respecified(job domain.Job, e domain.MessageReceived, p domain.Pipeline, reset bool) ([]domain.Event, error) {
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindSpec })
	if i < 0 {
		return nil, fmt.Errorf("decide: pipeline %q has no spec stage", p.Name)
	}
	evs := []domain.Event{domain.ReworkRequested{Job: job.ID, By: e.Author, Text: e.Text, At: e.At}}
	if reset {
		evs = append(evs, domain.SpecRejected{Job: job.ID, Rejections: 0, At: e.At})
	}
	return append(evs, domain.StageEntered{Job: job.ID, Stage: p.Stages[i].Name, At: e.At}), nil
}

// dropped ends an unmerged slice rejected on a person's direction and asks
// its initiative for a replan, which gets the direction. A slice asking to
// merge is not dropped, since the merge may already be under way, and a
// merged one keeps its feedback as an ordinary message.
func dropped(job domain.Job, e domain.MessageReceived) []domain.Event {
	return []domain.Event{
		domain.SliceDropped{Job: job.ID, Initiative: job.Initiative, By: e.Author, Text: e.Text, At: e.At},
		domain.ReplanRequested{Job: job.ID, Initiative: job.Initiative, At: e.At},
	}
}

// answered sends a job in needs-info back to the stage recorded as the one
// that asked, with the answer as its input. The recorded stage must be one of
// the pipeline's, so a corrupt job never jumps to a gate or a terminal state.
func answered(job domain.Job, e domain.MessageReceived, p domain.Pipeline) ([]domain.Event, error) {
	if !slices.ContainsFunc(p.Stages, func(s domain.Stage) bool { return s.Name == job.InfoFrom }) {
		return nil, fmt.Errorf("decide: needs-info asked by %q, not a stage of pipeline %q: %w", job.InfoFrom, p.Name, domain.ErrInvalidValue)
	}
	return []domain.Event{
		domain.InfoAnswered{Job: job.ID, By: e.Author, Text: e.Text, At: e.At},
		domain.StageEntered{Job: job.ID, Stage: job.InfoFrom, At: e.At},
	}, nil
}

// reviewed records the role's verdict and, once every role of the round has
// answered, moves on: to the next stage when no blocking role asked for
// CHANGES, back to on_changes for another round when one did, or to
// needs-human when that happens in the stage's last round. Round 1 runs every
// role of the stage; a later round runs only job.Reviewers, and the roles
// left out keep their earlier pass.
func reviewed(job domain.Job, e domain.ResultSubmitted, r domain.ReviewResult, p domain.Pipeline, i int) ([]domain.Event, error) {
	stage := p.Stages[i]
	round := stage.Roles
	if len(job.Reviewers) > 0 {
		round = job.Reviewers
	}
	if (len(job.Reviewers) > 0 && !slices.Contains(round, e.Role)) || slices.Contains(job.Passed, e.Role) || slices.Contains(job.Changes, e.Role) {
		return nil, illegal(job, e)
	}
	if r.Verdict == domain.ReviewChanges && stage.OnChanges == "" {
		return nil, fmt.Errorf("decide: review stage %q has no on_changes", stage.Name)
	}
	changes := job.Changes
	var evs []domain.Event
	if r.Verdict == domain.ReviewPass {
		evs = []domain.Event{domain.ReviewPassed{Job: job.ID, Role: e.Role, At: e.At}}
	} else {
		changes = append(slices.Clip(changes), e.Role)
		evs = []domain.Event{domain.ChangesRequested{Job: job.ID, Role: e.Role, Findings: r.Findings, At: e.At}}
	}
	for _, role := range round {
		if role != e.Role && !slices.Contains(job.Passed, role) && !slices.Contains(changes, role) {
			return evs, nil
		}
	}
	blocking := slices.DeleteFunc(slices.Clone(changes), func(role string) bool { return !blocks(p, role) })
	if len(blocking) == 0 {
		return append(evs, next(job, p, i, e.At)...), nil
	}
	if limit := roundLimit(job, p, stage); job.Round >= limit {
		reason := fmt.Sprintf("CHANGES in the last round (%d of %d) from %s", job.Round, limit, strings.Join(inOrder(stage.Roles, blocking), ", "))
		return append(evs, wall(job, p, domain.CauseChanges, reason, e.At)...), nil
	}
	return append(evs,
		domain.RoundStarted{Job: job.ID, Round: job.Round + 1, Reviewers: rerun(p, stage.Roles, changes), At: e.At},
		domain.StageEntered{Job: job.ID, Stage: stage.OnChanges, At: e.At},
	), nil
}

// blocks reports whether a CHANGES from role stops the job. Shadow and
// advisory roles never do (04-roles.md); a role with no mode, or not among
// the pipeline's roles, is blocking so pipelines built in code keep working.
func blocks(p domain.Pipeline, role string) bool {
	i := slices.IndexFunc(p.Roles, func(r domain.Role) bool { return r.Name == role })
	return i < 0 || (p.Roles[i].Mode != domain.ModeShadow && p.Roles[i].Mode != domain.ModeAdvisory)
}

// rerun lists, in stage order, the roles of the next round: every role that
// asked for CHANGES in this one, whatever its mode, plus the always_rerun ones.
func rerun(p domain.Pipeline, roles, changes []string) []string {
	var out []string
	for _, role := range roles {
		j := slices.IndexFunc(p.Roles, func(r domain.Role) bool { return r.Name == role })
		if slices.Contains(changes, role) || (j >= 0 && p.Roles[j].AlwaysRerun) {
			out = append(out, role)
		}
	}
	return out
}

// escalated is the needs-human reason for an approver ESCALATE: a person
// must be able to act on it, so it keeps every reason the approver gave.
func escalated(role string, reasons []string) string {
	if len(reasons) == 0 {
		return fmt.Sprintf("%s escalated", role)
	}
	return fmt.Sprintf("%s escalated: %s", role, strings.Join(reasons, "; "))
}

// blocked is the needs-human reason for a builder BLOCKED: the builder's
// summary, or which role blocked when it gave none, so the reason is never
// empty.
func blocked(role, summary string) string {
	if strings.TrimSpace(summary) == "" {
		return fmt.Sprintf("%s blocked", role)
	}
	return summary
}

// rejected is the reason a rejected job keeps: intake's own, trimmed, or a
// fixed one when it gave none, so the reason is never empty. The text is
// stored as data: Decide never parses it or routes on it.
func rejected(reason string) string {
	if r := strings.TrimSpace(reason); r != "" {
		return r
	}
	return "intake rejected"
}

// inOrder returns the roles in subset in the order the stage lists them, so
// the reason does not depend on the order reviewers answered in.
func inOrder(roles, subset []string) []string {
	var out []string
	for _, role := range roles {
		if slices.Contains(subset, role) {
			out = append(out, role)
		}
	}
	return out
}

// afterGate leaves a gate for the stage after the one of the given kind (a
// pipeline has one spec and one build stage, 04-roles.md).
func afterGate(job domain.Job, ev domain.Event, gate string, kind domain.Kind, at time.Time, p domain.Pipeline) ([]domain.Event, error) {
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == kind })
	if job.Stage != gate || i < 0 {
		return nil, illegal(job, ev)
	}
	return next(job, p, i, at), nil
}

// actionFailed stops the job for a person, whatever stage it is in, since
// the outside world refused what it asked for. A job already waiting in
// needs-human waits again for this cause, so an OK given for the old one
// never merges past the refusal.
func actionFailed(job domain.Job, e domain.ActionFailed) ([]domain.Event, error) {
	if domain.Blank(e.Key) {
		return nil, fmt.Errorf("decide: failed action without key: %w", domain.ErrMissingField)
	}
	reason := fmt.Sprintf("external action %s %q failed: %s", e.Kind, e.Key, e.Error)
	return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: reason, Cause: domain.CauseActionFailed, At: e.At}}, nil
}

// attemptFailed only records the failed attempt in the job's history: what
// the job does after one is not decided yet, so it stays where it is.
func attemptFailed(e domain.AttemptFailed) ([]domain.Event, error) {
	if err := domain.ValidateRoleName(e.Role); err != nil {
		return nil, fmt.Errorf("decide: failed attempt: %w", err)
	}
	if domain.Blank(e.Reason) {
		return nil, fmt.Errorf("decide: failed attempt without a reason: %w", domain.ErrMissingField)
	}
	return []domain.Event{e}, nil
}

// specgateFailed counts the rejection and sends the job back to the spec
// stage to be rewritten, or to needs-human on the last rejection the
// pipeline allows (limits.spec_rejections) so a spec that keeps failing never
// loops. A spec asking for a length exception is not a rejection: it waits for
// a person without counting. The review round does not change.
func specgateFailed(job domain.Job, e domain.SpecgateFailed, p domain.Pipeline) ([]domain.Event, error) {
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindSpec })
	if job.Stage != domain.StageSpecgate || i < 0 {
		return nil, illegal(job, e)
	}
	if e.Exception {
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: "spec asks for a length exception", Cause: domain.CauseSpecException, At: e.At}}, nil
	}
	n := job.SpecRejections + 1
	evs := []domain.Event{domain.SpecRejected{Job: job.ID, Rejections: n, At: e.At}}
	if n >= specRejections(job, p) {
		reason := fmt.Sprintf("spec rejected by specgate %d times", n)
		return append(evs, domain.NeedsHuman{Job: job.ID, Reason: reason, Cause: domain.CauseSpecEscalated, At: e.At}), nil
	}
	return append(evs, domain.StageEntered{Job: job.ID, Stage: p.Stages[i].Name, At: e.At}), nil
}

// gatePassed keeps the checks run with the job and moves it on from the gate
// to the stage after build.
func gatePassed(job domain.Job, e domain.GatePassed, p domain.Pipeline) ([]domain.Event, error) {
	evs, err := afterGate(job, e, domain.StageGate, domain.KindBuild, e.At, p)
	if err != nil {
		return nil, err
	}
	if err := e.Run.Validate(); err != nil {
		return nil, fmt.Errorf("decide: gate passed: %w", err)
	}
	return append([]domain.Event{checksRan(job, e.Run, e.At)}, evs...), nil
}

// gateFailed keeps the checks run with the job, so the next round's builder
// gets its log, and sends the job back to build for another round, or to
// needs-human when it fails in the last one. A failing gate uses up a round,
// as in the current factory, so the limit is the review stage's rounds; the
// new round keeps job.Reviewers because none of them got to run.
func gateFailed(job domain.Job, e domain.GateFailed, p domain.Pipeline) ([]domain.Event, error) {
	b := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindBuild })
	if job.Stage != domain.StageGate || b < 0 {
		return nil, illegal(job, e)
	}
	r := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindReview })
	if r < 0 {
		return nil, fmt.Errorf("decide: pipeline %q has no review stage", p.Name)
	}
	if err := e.Run.Validate(); err != nil {
		return nil, fmt.Errorf("decide: gate failed: %w", err)
	}
	ran := checksRan(job, e.Run, e.At)
	if limit := roundLimit(job, p, p.Stages[r]); job.Round >= limit {
		reason := fmt.Sprintf("gate failed in the last round (%d of %d)", job.Round, limit)
		return append([]domain.Event{ran}, wall(job, p, domain.CauseGateFailed, reason, e.At)...), nil
	}
	return []domain.Event{
		ran,
		domain.RoundStarted{Job: job.ID, Round: job.Round + 1, Reviewers: slices.Clone(job.Reviewers), At: e.At},
		domain.StageEntered{Job: job.ID, Stage: p.Stages[b].Name, At: e.At},
	}, nil
}

// checksRan is the history entry for the checks run the gate judged in the
// job's current round.
func checksRan(job domain.Job, run domain.ChecksRun, at time.Time) domain.ChecksRan {
	return domain.ChecksRan{Job: job.ID, Round: job.Round, Run: run, At: at}
}

// budgetExhausted stops a job in a pipeline stage or in triage, the only
// stages that launch agents, until a person acts. What it spent is recorded,
// since a raise of its budget must pass it.
func budgetExhausted(job domain.Job, e domain.BudgetExhausted, p domain.Pipeline) ([]domain.Event, error) {
	if job.Stage != domain.StageTriage && !slices.ContainsFunc(p.Stages, func(s domain.Stage) bool { return s.Name == job.Stage }) {
		return nil, illegal(job, e)
	}
	if !positive(e.Spent) {
		return nil, fmt.Errorf("decide: spent budget of USD %v: %w", e.Spent, domain.ErrMissingField)
	}
	return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: "budget exhausted", Cause: domain.CauseBudget, Spent: e.Spent, At: e.At}}, nil
}

// capRaised decides an allowed person's raise of a cap on the job. Only the
// job's own budget is the job's to raise, and only while it waits in
// needs-human for having spent it; the issue's slices and USD are raised
// where the issue's next slice starts. The new budget must pass what the job
// spent, and the job goes back to the stage it was in, after the history
// entry. Every refusal carries its code and resumes nothing.
func capRaised(job domain.Job, e domain.CapRaiseRequested, p domain.Pipeline) ([]domain.Event, error) {
	if domain.Blank(e.By) {
		return nil, domain.RefuseRaise(domain.RaiseNoAuthor, "raise without who asked")
	}
	if err := e.Cap.Validate(); err != nil {
		return nil, domain.RefuseRaise(domain.RaiseUnknownCap, "%v", err)
	}
	if !positive(e.Value) {
		return nil, domain.RefuseRaise(domain.RaiseBadValue, "%s to %v", e.Cap, e.Value)
	}
	// Checked before the stage, as for any person's action, so a stale
	// raise tells nothing about the job.
	if e.WaitSeq == 0 || e.WaitSeq != job.WaitSeq {
		return nil, domain.RefuseRaise(domain.RaiseStaleWait, "raise for wait %d, expected wait %d", e.WaitSeq, job.WaitSeq)
	}
	if e.Cap != domain.CapBudget || job.Stage != domain.StageNeedsHuman || job.Cause != domain.CauseBudget || job.Paused == nil {
		return nil, domain.RefuseRaise(domain.RaiseNotStopped, "%s in stage %q, cause %q", e.Cap, job.Stage, job.Cause)
	}
	if e.Value <= job.Paused.Spent {
		return nil, domain.RefuseRaise(domain.RaiseNotAbove, "%s to USD %v, spent USD %v", e.Cap, e.Value, job.Paused.Spent)
	}
	raised := domain.CapRaised{Job: job.ID, By: e.By, Cap: e.Cap, Value: e.Value, At: e.At}
	at := *job.Paused
	if at.Stage == domain.StageTriage {
		if err := at.Wall.Validate(); err != nil {
			return nil, fmt.Errorf("decide: budget raise back to triage: wall: %w", err)
		}
		return []domain.Event{raised, domain.TriageRequested{Job: job.ID, Reason: at.Reason, Cause: at.Wall, At: e.At}}, nil
	}
	if !slices.ContainsFunc(p.Stages, func(s domain.Stage) bool { return s.Name == at.Stage }) {
		return nil, fmt.Errorf("decide: budget raise back to %q, not a stage of pipeline %q: %w", at.Stage, p.Name, domain.ErrInvalidValue)
	}
	return []domain.Event{raised, domain.StageEntered{Job: job.ID, Stage: at.Stage, At: e.At}}, nil
}

// positive reports whether v is a finite amount above 0.
func positive(v float64) bool {
	return v > 0 && !math.IsInf(v, 1)
}

// next enters the stage after stage i, or requests the merge after the last.
func next(job domain.Job, p domain.Pipeline, i int, at time.Time) []domain.Event {
	if i+1 < len(p.Stages) {
		return []domain.Event{domain.StageEntered{Job: job.ID, Stage: p.Stages[i+1].Name, At: at}}
	}
	return []domain.Event{domain.MergeRequested{Job: job.ID, At: at}}
}

// isNil reports whether v is a nil interface or holds a nil pointer: calling
// a value method through a nil pointer panics, so Decide refuses both.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

func illegal(job domain.Job, ev domain.Event) error {
	return fmt.Errorf("%w: %s in stage %q", ErrIllegalTransition, ev.EventName(), job.Stage)
}
