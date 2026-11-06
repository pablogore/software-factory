package decide

import (
	"errors"
	"fmt"
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
// and done.
var ErrUnsupported = errors.New("unsupported result")

// ErrOtherJob means the event is addressed to a different job than the one
// it was decided for. Decide returns no events with it, so nothing changes.
var ErrOtherJob = errors.New("event for another job")

// ErrStopped means a person stopped the job, so no event moves it any more.
// Decide returns no events with it, so nothing changes.
var ErrStopped = errors.New("job stopped")

// Decide returns the events that follow ev for job in pipeline p. It never
// reads a clock: every event it returns carries the time of ev.
func Decide(job domain.Job, ev domain.Event, p domain.Pipeline) ([]domain.Event, error) {
	if isNil(ev) {
		return nil, fmt.Errorf("%w: no event in stage %q", ErrIllegalTransition, job.Stage)
	}
	if ev.JobID() != job.ID {
		return nil, fmt.Errorf("%w: %s for job %q decided for job %q", ErrOtherJob, ev.EventName(), ev.JobID(), job.ID)
	}
	if m, ok := ev.(domain.MessageReceived); ok && ignored(m) {
		return nil, nil
	}
	if job.Stage == domain.StageStopped {
		return nil, fmt.Errorf("%w: %s", ErrStopped, ev.EventName())
	}
	if job.Stage == domain.StageRejected {
		return nil, illegal(job, ev)
	}
	switch e := ev.(type) {
	case domain.StopRequested:
		if strings.TrimSpace(e.By) == "" {
			return nil, fmt.Errorf("decide: stop request without who asked: %w", domain.ErrMissingField)
		}
		return []domain.Event{domain.JobStopped{Job: job.ID, By: e.By, At: e.At}}, nil
	case domain.ResultSubmitted:
		return decideResult(job, e, p)
	case domain.SpecgatePassed:
		return afterGate(job, ev, domain.StageSpecgate, domain.KindSpec, e.At, p)
	case domain.GatePassed:
		return afterGate(job, ev, domain.StageGate, domain.KindBuild, e.At, p)
	case domain.SpecgateFailed:
		return specgateFailed(job, e, p)
	case domain.GateFailed:
		return gateFailed(job, e, p)
	case domain.HumanApproved:
		if strings.TrimSpace(e.By) == "" {
			return nil, fmt.Errorf("decide: approval without who gave it: %w", domain.ErrMissingField)
		}
		// An OK on a length exception passes specgate with the spec as it is.
		if job.Stage == domain.StageNeedsHuman && job.SpecException {
			return afterGate(job, ev, domain.StageNeedsHuman, domain.KindSpec, e.At, p)
		}
		if job.Stage == domain.StageNeedsHuman {
			return []domain.Event{domain.MergeRequested{Job: job.ID, At: e.At}}, nil
		}
	case domain.MergeCompleted:
		if job.Stage == domain.StageMerge {
			evs := []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageDone, At: e.At}}
			if job.Initiative != "" {
				evs = append(evs, domain.ReplanRequested{Job: job.ID, Initiative: job.Initiative, At: e.At})
			}
			return evs, nil
		}
	case domain.MessageReceived:
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
		case domain.NeedsInfo:
			job.Stage, job.InfoFrom, job.Passed, job.Changes = domain.StageNeedsInfo, e.From, nil, nil
			job = leftNeedsHuman(job)
		case domain.ReviewPassed:
			job.Passed = append(slices.Clip(job.Passed), e.Role)
		case domain.ChangesRequested:
			job.Changes = append(slices.Clip(job.Changes), e.Role)
		case domain.SpecRejected:
			job.SpecRejections = e.Rejections
		case domain.RoundStarted:
			job.Round, job.Reviewers, job.Passed, job.Changes = e.Round, e.Reviewers, nil, nil
		case domain.NeedsHuman:
			job.Stage, job.InfoFrom, job.Reviewers, job.Passed, job.Changes = domain.StageNeedsHuman, "", nil, nil, nil
			job.SpecException, job.SpecEscalated, job.Reason = e.SpecException, e.SpecEscalated, e.Reason
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

// leftNeedsHuman forgets why the job was in needs-human, so a later stop for
// a person never inherits an old cause.
func leftNeedsHuman(job domain.Job) domain.Job {
	job.SpecException, job.SpecEscalated, job.Reason = false, false, ""
	return job
}

func decideResult(job domain.Job, e domain.ResultSubmitted, p domain.Pipeline) ([]domain.Event, error) {
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
			return []domain.Event{domain.JobRejected{Job: job.ID, Reason: rejected(r.Reason), At: e.At}}, nil
		}
	case domain.SpecResult:
		return []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageSpecgate, At: e.At}}, nil
	case domain.BuildResult:
		if r.Status == domain.BuildReady {
			return []domain.Event{domain.StageEntered{Job: job.ID, Stage: domain.StageGate, At: e.At}}, nil
		}
		// No pipeline has a triage stage yet (E02), so a blocked build waits
		// for a person, whose feedback sends it back to build.
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: blocked(e.Role, r.Summary), At: e.At}}, nil
	case domain.ReviewResult:
		return reviewed(job, e, r, p, i)
	case domain.ApproveResult:
		if r.Decision == domain.ApproveApprove {
			return next(job, p, i, e.At), nil
		}
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: escalated(e.Role, r.Reasons), At: e.At}}, nil
	}
	return nil, fmt.Errorf("%w: %s result from %q in stage %q", ErrUnsupported, stage.Kind, e.Role, job.Stage)
}

// ignored reports whether Decide drops a message without error: the event
// source did not mark it as coming from an allowed person, so bots, CI and
// strangers change nothing, in every stage.
func ignored(e domain.MessageReceived) bool {
	return !e.Allowed || e.AuthorKind != domain.AuthorPerson
}

// decideMessage handles an allowed person's message. Marked as a direction on
// an unmerged slice, it drops the slice. In needs-info it is the
// answer, so the job goes back to the stage that asked. In needs-human on a
// length exception or marked as stopped by specgate's last rejection (not
// read from the count, which can be stale) it is feedback for the spec, which
// is rewritten with it. Otherwise in needs-human it is rework feedback, and in done it
// reopens the merged job: either way the job goes back to the build stage in
// the same round, with the text as input, so a reopened job past its last
// round still needs a person after CHANGES.
func decideMessage(job domain.Job, e domain.MessageReceived, p domain.Pipeline) ([]domain.Event, error) {
	if e.Direction && job.Initiative != "" && job.Stage != domain.StageMerge && job.Stage != domain.StageDone {
		return dropped(job, e), nil
	}
	if job.Stage == domain.StageNeedsInfo {
		return answered(job, e, p)
	}
	if job.Stage != domain.StageNeedsHuman && job.Stage != domain.StageDone {
		return nil, fmt.Errorf("%w: message from %q in stage %q", ErrUnsupported, e.Author, job.Stage)
	}
	if job.Stage == domain.StageNeedsHuman && job.SpecException {
		return respecified(job, e, p, false)
	}
	if job.Stage == domain.StageNeedsHuman && job.SpecEscalated {
		return respecified(job, e, p, true)
	}
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindBuild })
	if i < 0 {
		return nil, fmt.Errorf("decide: pipeline %q has no build stage", p.Name)
	}
	return []domain.Event{
		domain.ReworkRequested{Job: job.ID, By: e.Author, Text: e.Text, At: e.At},
		domain.StageEntered{Job: job.ID, Stage: p.Stages[i].Name, At: e.At},
	}, nil
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
	if job.Round >= stage.MaxRounds {
		reason := fmt.Sprintf("CHANGES in the last round (%d of %d) from %s", job.Round, stage.MaxRounds, strings.Join(inOrder(stage.Roles, blocking), ", "))
		return append(evs, domain.NeedsHuman{Job: job.ID, Reason: reason, At: e.At}), nil
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

// maxSpecRejections is how many specgate rejections a job takes before a
// person must look at it, fixed as in the current factory.
const maxSpecRejections = 3

// specgateFailed counts the rejection and sends the job back to the spec
// stage to be rewritten, or to needs-human on the last allowed rejection so
// a spec that keeps failing never loops. A spec asking for a length exception
// is not a rejection: it waits for a person without counting. The review
// round does not change.
func specgateFailed(job domain.Job, e domain.SpecgateFailed, p domain.Pipeline) ([]domain.Event, error) {
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindSpec })
	if job.Stage != domain.StageSpecgate || i < 0 {
		return nil, illegal(job, e)
	}
	if e.Exception {
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: "spec asks for a length exception", SpecException: true, At: e.At}}, nil
	}
	n := job.SpecRejections + 1
	evs := []domain.Event{domain.SpecRejected{Job: job.ID, Rejections: n, At: e.At}}
	if n >= maxSpecRejections {
		reason := fmt.Sprintf("spec rejected by specgate %d times", n)
		return append(evs, domain.NeedsHuman{Job: job.ID, Reason: reason, SpecEscalated: true, At: e.At}), nil
	}
	return append(evs, domain.StageEntered{Job: job.ID, Stage: p.Stages[i].Name, At: e.At}), nil
}

// gateFailed sends the job back to build for another round, or to needs-human
// when it fails in the last one. A failing gate uses up a round, as in the
// current factory, so the limit is the review stage's max_rounds; the new
// round keeps job.Reviewers because none of them got to run.
func gateFailed(job domain.Job, e domain.GateFailed, p domain.Pipeline) ([]domain.Event, error) {
	b := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindBuild })
	if job.Stage != domain.StageGate || b < 0 {
		return nil, illegal(job, e)
	}
	r := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Kind == domain.KindReview })
	if r < 0 {
		return nil, fmt.Errorf("decide: pipeline %q has no review stage", p.Name)
	}
	if limit := p.Stages[r].MaxRounds; job.Round >= limit {
		reason := fmt.Sprintf("gate failed in the last round (%d of %d)", job.Round, limit)
		return []domain.Event{domain.NeedsHuman{Job: job.ID, Reason: reason, At: e.At}}, nil
	}
	return []domain.Event{
		domain.RoundStarted{Job: job.ID, Round: job.Round + 1, Reviewers: slices.Clone(job.Reviewers), At: e.At},
		domain.StageEntered{Job: job.ID, Stage: p.Stages[b].Name, At: e.At},
	}, nil
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
