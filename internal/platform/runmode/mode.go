// Package runmode is how a factory run moves between issues. In the default
// mode it brakes between them: when an issue ends (its initiative DONE, a job
// on its own merged or rejected) or stops for a person (a job in needs-human
// or needs-info, an initiative in needs-human), no new issue starts. What already runs ends, and the slices
// of an initiative already running go on by its checkpoint, since they never
// pass through the queue.
//
// On braking it publishes a summary of the issue, with the next issue the
// queue would take, skipping the ones it holds, and why, as a code each
// channel words in its language, to the thread the issue came from, through the
// outbox, and to the inbox.
//
// Nothing calls it yet: the daemon will subscribe Mode.Handle to the bus.
package runmode

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/ports"
)

// ActionSummary is the kind of the outbox action that posts a summary to the
// issue's origin thread.
const ActionSummary = "summary"

// Events names the events Mode.Handle must be subscribed to.
var Events = []string{"InitiativeEnded", "InitiativeNeedsHumanRaised", "NeedsHuman", "NeedsInfo", "JobRejected", "StageEntered"}

// Reports tells what became of the issue a job belongs to.
type Reports interface {
	// Report returns the issue of job and its jobs, job among them.
	Report(ctx context.Context, job domain.JobID) (IssueReport, error)
}

// Outbox saves external actions to be delivered.
type Outbox interface {
	// Enqueue saves acts as job's pending actions. An action whose Key is
	// already saved is kept once and is not an error.
	Enqueue(ctx context.Context, job domain.JobID, acts ...ports.Action) error
}

// Inbox keeps what `factory inbox` shows.
type Inbox interface {
	// Put saves s under key. Putting a saved key again keeps one entry.
	Put(ctx context.Context, key string, s Summary) error
}

// Queue is what the mode needs from internal/platform/queue.
type Queue interface {
	Add(ctx context.Context, issues ...queue.Issue)
	Waiting() []queue.Issue
	Held() []queue.Hold
}

// Mode brakes the run between issues and publishes the summary.
type Mode struct {
	brake   *Brake
	queue   Queue
	order   queue.Order
	reports Reports
	outbox  Outbox
	inbox   Inbox
}

// New returns the default mode: it puts brake on, which must be the
// scheduler q was built on, and recommends the next issue by order, the
// one q takes issues in.
func New(brake *Brake, q Queue, order queue.Order, reports Reports, outbox Outbox, inbox Inbox) *Mode {
	return &Mode{brake: brake, queue: q, order: order, reports: reports, outbox: outbox, inbox: inbox}
}

// Brakes tells whether ev, saved for job, ends job's issue or stops job for
// a person. A slice rejected or merged does not: its initiative replans.
func Brakes(ev domain.Event, job domain.Job) bool {
	switch e := ev.(type) {
	case domain.InitiativeEnded, domain.InitiativeNeedsHumanRaised, domain.NeedsHuman, domain.NeedsInfo:
		return true
	case domain.JobRejected:
		return job.Initiative == ""
	case domain.StageEntered:
		return e.Stage == domain.StageDone && job.Initiative == ""
	}
	return false
}

// Handle puts the brake on when ev brakes, then publishes the summary of
// its issue. It is idempotent: a summary handed over again is kept once.
func (m *Mode) Handle(ctx context.Context, ev domain.Event) error {
	if !slices.Contains(Events, ev.EventName()) {
		return nil
	}
	r, err := m.reports.Report(ctx, ev.JobID())
	if err != nil {
		return fmt.Errorf("runmode: report of %q: %w", ev.JobID(), err)
	}
	i := slices.IndexFunc(r.Jobs, func(j JobReport) bool { return j.Job.ID == ev.JobID() })
	if i < 0 {
		return fmt.Errorf("runmode: report of issue #%d without job %q: %w", r.Issue.Number, ev.JobID(), domain.ErrMissingField)
	}
	job := r.Jobs[i].Job
	if !Brakes(ev, job) {
		return nil
	}
	m.brake.Engage()

	s := Summarize(r, job.ID, m.queue.Waiting(), m.queue.Held(), m.order)
	payload, err := json.Marshal(struct {
		Origin  domain.Origin `json:"origin"`
		Summary Summary       `json:"summary"`
	}{job.Origin, s})
	if err != nil {
		return fmt.Errorf("runmode: summary of issue #%d: %w", s.Issue, err)
	}
	key := fmt.Sprintf("%s/summary/%s/%d", job.ID, ev.EventName(), at(ev).UnixNano())
	if err := m.outbox.Enqueue(ctx, job.ID, ports.Action{Key: key, Kind: ActionSummary, Payload: string(payload)}); err != nil {
		return fmt.Errorf("runmode: summary of issue #%d: %w", s.Issue, err)
	}
	if err := m.inbox.Put(ctx, key, s); err != nil {
		return fmt.Errorf("runmode: summary of issue #%d: %w", s.Issue, err)
	}
	return nil
}

// Release is the operator's continue: the brake comes off, the issue it
// held back starts and the queue hands the next ones over.
func (m *Mode) Release(ctx context.Context) {
	m.brake.Release()
	m.queue.Add(ctx)
}

// at is when ev, one of Events, happened.
func at(ev domain.Event) time.Time {
	switch e := ev.(type) {
	case domain.InitiativeEnded:
		return e.At
	case domain.InitiativeNeedsHumanRaised:
		return e.At
	case domain.NeedsHuman:
		return e.At
	case domain.NeedsInfo:
		return e.At
	case domain.JobRejected:
		return e.At
	case domain.StageEntered:
		return e.At
	}
	return time.Time{}
}
