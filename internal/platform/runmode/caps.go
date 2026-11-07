package runmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
)

// ActionCapped is the kind of the outbox action that posts why an issue
// stopped at a cap to its origin thread.
const ActionCapped = "issue-capped"

// ErrCapped means the issue is past one of its caps, so its next slice did
// not start.
var ErrCapped = errors.New("runmode: issue past its cap")

// Caps are the run's caps per issue, so no issue chains slices or spends
// without end. They count every job of the issue, spikes included, and hold
// in every mode.
type Caps struct {
	// Slices is how many jobs an issue may have; it starts no slice once
	// it has that many.
	Slices int
	// USD is how much an issue's jobs may cost; it starts no slice once
	// they cost more.
	USD float64
}

// DefaultCaps are the caps of a run whose config sets none; a config that
// sets one of them starts from these.
var DefaultCaps = Caps{Slices: 5, USD: 8}

// Validate returns an error naming the first cap under 1: a cap of 0 would
// stop every issue before its first slice.
func (c Caps) Validate() error {
	if c.Slices < 1 {
		return fmt.Errorf("runmode: cap slices is %d, must be at least 1: %w", c.Slices, domain.ErrInvalidValue)
	}
	if c.USD < 1 {
		return fmt.Errorf("runmode: cap usd is %v, must be at least 1: %w", c.USD, domain.ErrInvalidValue)
	}
	return nil
}

// Stop is why an issue starts no more slices: the cap it reached, the cap's
// value and the value the issue reached.
type Stop struct {
	Issue   int             `json:"issue"`
	Title   string          `json:"title"`
	Cap     domain.IssueCap `json:"cap"`
	Limit   float64         `json:"limit"`
	Reached float64         `json:"reached"`
}

// StopInbox keeps the stops `factory inbox` shows.
type StopInbox interface {
	// PutStop saves s under key. Putting a saved key again keeps one entry.
	PutStop(ctx context.Context, key string, s Stop) error
}

// Slots runs work in the scheduler's slots.
type Slots interface {
	Submit(ctx context.Context, job domain.JobID, w scheduler.Work) <-chan error
}

// Capped returns why r's issue must start no more slices under c, or false
// when it may go on. The slice cap is checked first.
func Capped(r IssueReport, c Caps) (Stop, bool) {
	stop := Stop{Issue: r.Issue.Number, Title: r.Issue.Title}
	usd := 0.0
	for _, j := range r.Jobs {
		usd += j.USD
	}
	switch {
	case len(r.Jobs) >= c.Slices:
		stop.Cap, stop.Limit, stop.Reached = domain.CapSlices, float64(c.Slices), float64(len(r.Jobs))
	case usd > c.USD:
		stop.Cap, stop.Limit, stop.Reached = domain.CapUSD, c.USD, usd
	default:
		return Stop{}, false
	}
	return stop, true
}

// Starter starts an initiative's next slice unless its issue is past a cap.
// Slices go to the scheduler directly, never through the brake, and a capped
// issue stops only itself: every other issue goes on.
type Starter struct {
	slots   Slots
	caps    Caps
	reports Reports
	outbox  Outbox
	inbox   StopInbox
}

// NewStarter returns a starter that runs slices in slots under caps, or an
// error when caps does not validate.
func NewStarter(slots Slots, caps Caps, reports Reports, outbox Outbox, inbox StopInbox) (*Starter, error) {
	if err := caps.Validate(); err != nil {
		return nil, err
	}
	return &Starter{slots: slots, caps: caps, reports: reports, outbox: outbox, inbox: inbox}, nil
}

// Start hands w, job's work as the slice ev asks for, to the scheduler, as
// Submit does. When ev's issue is past a cap it starts nothing, publishes
// the stop to the issue's origin thread, through the outbox, and to the
// inbox, and returns ErrCapped. It is idempotent: a stop published again is
// kept once.
func (s *Starter) Start(ctx context.Context, ev domain.NextSliceRequested, job domain.JobID, w scheduler.Work) (<-chan error, error) {
	r, err := s.reports.Report(ctx, ev.Job)
	if err != nil {
		return nil, fmt.Errorf("runmode: report of %q: %w", ev.Job, err)
	}
	stop, capped := Capped(r, s.caps)
	if !capped {
		return s.slots.Submit(ctx, job, w), nil
	}
	i := slices.IndexFunc(r.Jobs, func(j JobReport) bool { return j.Job.ID == ev.Job })
	if i < 0 {
		return nil, fmt.Errorf("runmode: report of issue #%d without job %q: %w", r.Issue.Number, ev.Job, domain.ErrMissingField)
	}
	payload, err := json.Marshal(struct {
		Origin domain.Origin `json:"origin"`
		Stop   Stop          `json:"stop"`
	}{r.Jobs[i].Job.Origin, stop})
	if err != nil {
		return nil, fmt.Errorf("runmode: stop of issue #%d: %w", stop.Issue, err)
	}
	key := fmt.Sprintf("%s/capped/%s/%d", ev.Job, stop.Cap, ev.At.UnixNano())
	if err := s.outbox.Enqueue(ctx, ev.Job, ports.Action{Key: key, Kind: ActionCapped, Payload: string(payload)}); err != nil {
		return nil, fmt.Errorf("runmode: stop of issue #%d: %w", stop.Issue, err)
	}
	if err := s.inbox.PutStop(ctx, key, stop); err != nil {
		return nil, fmt.Errorf("runmode: stop of issue #%d: %w", stop.Issue, err)
	}
	return nil, fmt.Errorf("issue #%d, %s %v of %v: %w", stop.Issue, stop.Cap, stop.Reached, stop.Limit, ErrCapped)
}
