package runmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"

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
// value and the value the issue reached. WaitSeq numbers the issue's stops,
// from 1: a person's raise names the one it answers.
type Stop struct {
	Issue   int             `json:"issue"`
	Title   string          `json:"title"`
	Cap     domain.IssueCap `json:"cap"`
	Limit   float64         `json:"limit"`
	Reached float64         `json:"reached"`
	WaitSeq int             `json:"wait_seq,omitempty"`
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

// Slicer builds the work of a slice job, so a slice an issue held across a
// restart of factoryd can start once a person raises its cap.
type Slicer interface {
	// Work is job's work as a slice of its initiative.
	Work(ctx context.Context, job domain.JobID) (scheduler.Work, error)
}

// CapLog keeps each issue's raised caps and stop across restarts of
// factoryd.
type CapLog interface {
	// SaveIssueCaps saves c for issue c.Issue, replacing what was saved.
	SaveIssueCaps(ctx context.Context, c ports.IssueCaps) error
	// IssueCaps is what SaveIssueCaps saved last for issue n, or n with
	// nothing raised and not stopped.
	IssueCaps(ctx context.Context, n int) (ports.IssueCaps, error)
}

// Starter starts an initiative's next slice unless its issue is past a cap.
// Slices go to the scheduler directly, never through the brake, and a capped
// issue stops only itself: every other issue goes on. A stopped issue starts
// slices again only when an allowed person raises the cap it stopped at
// (Raise). The stops, their waits and the raised caps are saved in
// factoryd's store (CapLog), so a restart changes none of them.
type Starter struct {
	slots   Slots
	slicer  Slicer
	caps    Caps
	reports Reports
	outbox  Outbox
	inbox   StopInbox
	log     CapLog

	// mu orders each issue's reads and saves of its caps, so starts and
	// raises may come from several goroutines.
	mu sync.Mutex
}

// NewStarter returns a starter that runs slices, as slicer builds them, in
// slots under caps, keeping each issue's caps in log, or an error when caps
// does not validate.
func NewStarter(slots Slots, slicer Slicer, caps Caps, reports Reports, outbox Outbox, inbox StopInbox, log CapLog) (*Starter, error) {
	if err := caps.Validate(); err != nil {
		return nil, err
	}
	return &Starter{slots: slots, slicer: slicer, caps: caps, reports: reports, outbox: outbox, inbox: inbox, log: log}, nil
}

// Start hands job's work, as the slice ev asks for, to the scheduler, as
// Submit does. When ev's issue is past a cap it starts nothing, publishes
// the stop to the issue's origin thread, through the outbox, and to the
// inbox, and returns ErrCapped. It is idempotent: a stop published again is
// kept once, under the same wait.
func (s *Starter) Start(ctx context.Context, ev domain.NextSliceRequested, job domain.JobID) (<-chan error, error) {
	r, err := s.reports.Report(ctx, ev.Job)
	if err != nil {
		return nil, fmt.Errorf("runmode: report of %q: %w", ev.Job, err)
	}
	n := r.Issue.Number
	s.mu.Lock()
	ic, err := s.log.IssueCaps(ctx, n)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("runmode: caps of issue #%d: %w", n, err)
	}
	stop, capped := Capped(r, s.capsOf(ic))
	if !capped {
		if ic.Stop != nil {
			ic.Stop = nil
			if err := s.log.SaveIssueCaps(ctx, ic); err != nil {
				s.mu.Unlock()
				return nil, fmt.Errorf("runmode: caps of issue #%d: %w", n, err)
			}
		}
		s.mu.Unlock()
		w, err := s.slicer.Work(ctx, job)
		if err != nil {
			return nil, fmt.Errorf("runmode: work of %q: %w", job, err)
		}
		return s.slots.Submit(ctx, job, w), nil
	}
	// The same stop published again keeps its wait, so the outbox and the
	// inbox keep one entry that agree.
	if h := ic.Stop; h != nil && h.Next.Job == ev.Job && h.Next.Initiative == ev.Initiative && h.Next.At.Equal(ev.At) && h.Job == job && h.Cap == stop.Cap {
		stop.WaitSeq = h.WaitSeq
	} else {
		ic.Waits++
		stop.WaitSeq = ic.Waits
	}
	ic.Stop = &ports.CapStop{Next: ev, Job: job, Cap: stop.Cap, Reached: stop.Reached, WaitSeq: stop.WaitSeq}
	if err := s.log.SaveIssueCaps(ctx, ic); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("runmode: stop of issue #%d: %w", n, err)
	}
	s.mu.Unlock()
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

// capsOf is the caps an issue starts slices under: the run's, with the ones
// a person raised for it.
func (s *Starter) capsOf(ic ports.IssueCaps) Caps {
	c := s.caps
	if ic.Slices > 0 {
		c.Slices = ic.Slices
	}
	if ic.USD > 0 {
		c.USD = ic.USD
	}
	return c
}

// Raise acts on a person's raise of a cap of e.Job's issue. An allowed
// person's raise of the cap the issue is stopped at, answering that stop's
// WaitSeq and passing the value it reached, raises the cap for that issue
// alone and starts the slice it held, as Start does: the issue may stop
// again at its other cap. A raise not from an allowed person changes nothing
// and is not an error; any other invalid one starts nothing and returns a
// domain.RaiseRefusedError with its code. The job's own budget is not the
// issue's to raise here: decide.Decide raises it.
func (s *Starter) Raise(ctx context.Context, e domain.CapRaiseRequested) (<-chan error, error) {
	if e.AuthorKind != domain.AuthorPerson || !e.Allowed {
		return nil, nil
	}
	if domain.Blank(e.By) {
		return nil, domain.RefuseRaise(domain.RaiseNoAuthor, "raise without who asked")
	}
	if err := e.Cap.Validate(); err != nil {
		return nil, domain.RefuseRaise(domain.RaiseUnknownCap, "%v", err)
	}
	if !(e.Value > 0) || math.IsInf(e.Value, 1) || (e.Cap == domain.CapSlices && (e.Value != math.Trunc(e.Value) || e.Value > math.MaxInt32)) {
		return nil, domain.RefuseRaise(domain.RaiseBadValue, "%s to %v", e.Cap, e.Value)
	}
	r, err := s.reports.Report(ctx, e.Job)
	if err != nil {
		return nil, fmt.Errorf("runmode: report of %q: %w", e.Job, err)
	}
	n := r.Issue.Number
	s.mu.Lock()
	ic, err := s.log.IssueCaps(ctx, n)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("runmode: caps of issue #%d: %w", n, err)
	}
	h := ic.Stop
	switch {
	case h == nil:
		s.mu.Unlock()
		return nil, domain.RefuseRaise(domain.RaiseNotStopped, "issue #%d is not stopped", n)
	case e.WaitSeq == 0 || e.WaitSeq != h.WaitSeq:
		s.mu.Unlock()
		return nil, domain.RefuseRaise(domain.RaiseStaleWait, "raise for wait %d, expected wait %d", e.WaitSeq, h.WaitSeq)
	case e.Cap != h.Cap:
		s.mu.Unlock()
		return nil, domain.RefuseRaise(domain.RaiseNotStopped, "issue #%d is stopped at %s, not %s", n, h.Cap, e.Cap)
	case e.Value <= h.Reached:
		s.mu.Unlock()
		return nil, domain.RefuseRaise(domain.RaiseNotAbove, "%s to %v, reached %v", e.Cap, e.Value, h.Reached)
	}
	if e.Cap == domain.CapSlices {
		ic.Slices = int(e.Value)
	} else {
		ic.USD = e.Value
	}
	ic.Stop = nil
	if err := s.log.SaveIssueCaps(ctx, ic); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("runmode: raise of issue #%d: %w", n, err)
	}
	s.mu.Unlock()
	return s.Start(ctx, h.Next, h.Job)
}
