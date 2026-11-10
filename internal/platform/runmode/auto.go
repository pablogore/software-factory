package runmode

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
)

// Limits are the caps of a run in the auto mode, so an unattended run ends
// on its own.
type Limits struct {
	// Issues is how many issues the run may finish (--max-jobs); it starts
	// no new issue once it finished that many.
	Issues int
	// USD is how much the run may spend (--max-usd); it starts no new issue
	// once its issues cost that much.
	USD float64
}

// DefaultLimits are the caps of an auto run that sets none; a run that sets
// one of them starts from these.
var DefaultLimits = Limits{Issues: 5, USD: 20}

// Validate returns an error naming the first cap under 1: a cap of 0 would
// end the run before its first issue.
func (l Limits) Validate() error {
	if l.Issues < 1 {
		return fmt.Errorf("runmode: max-jobs is %d, must be at least 1: %w", l.Issues, domain.ErrInvalidValue)
	}
	if l.USD < 1 {
		return fmt.Errorf("runmode: max-usd is %v, must be at least 1: %w", l.USD, domain.ErrInvalidValue)
	}
	return nil
}

// RunStop is why an auto run starts no new issue: the cap it reached, the
// cap's value and the value the run reached.
type RunStop struct {
	Cap     domain.RunCap `json:"cap"`
	Limit   float64       `json:"limit"`
	Reached float64       `json:"reached"`
}

// RunCapped returns the cap under l that a run which finished issues and
// spent usd reached, or false when it may go on. The issue cap is checked
// first.
func RunCapped(issues int, usd float64, l Limits) (RunStop, bool) {
	switch {
	case issues >= l.Issues:
		return RunStop{Cap: domain.CapRunIssues, Limit: float64(l.Issues), Reached: float64(issues)}, true
	case usd >= l.USD:
		return RunStop{Cap: domain.CapRunUSD, Limit: l.USD, Reached: usd}, true
	}
	return RunStop{}, false
}

// NewAuto returns the auto mode: it does not brake when an issue ends, only
// when a job or an initiative stops for a person, as the default mode, or
// when the run reaches one of l's caps. The other arguments are New's.
func NewAuto(ctx context.Context, brake *Brake, q Queue, order queue.Order, reports Reports, outbox Outbox, inbox Inbox, log BrakeLog, l Limits) (*Mode, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	m, err := New(ctx, brake, q, order, reports, outbox, inbox, log)
	if err != nil {
		return nil, err
	}
	m.auto = &tally{limits: l, ended: map[int]bool{}, spent: map[int]float64{}}
	return m, nil
}

// tally is what an auto run counts towards its caps.
type tally struct {
	limits Limits
	// ended lists the issues the run finished, so an event handed over
	// again counts once.
	ended map[int]bool
	// spent is what each issue seen in the run cost, at its last event, so
	// issues still running count too.
	spent map[int]float64
	// crossed is the key of the event that took the run past a cap, so a
	// retry of it publishes again; empty while under the caps.
	crossed string
}

// forPerson tells whether ev stops its job or initiative for a person.
func forPerson(ev domain.Event) bool {
	switch ev.(type) {
	case domain.InitiativeNeedsHumanRaised, domain.NeedsHuman, domain.NeedsInfo:
		return true
	}
	return false
}

// count adds what ev, saved for job of r under key, tells of the run, and
// returns whether it brakes, with the cap the run reached, if any. Past a
// cap, the event that crossed it and every issue that ends brake.
func (t *tally) count(ev domain.Event, job domain.Job, r IssueReport, key string) (*RunStop, bool) {
	ends := Brakes(ev, job) && !forPerson(ev)
	if ends {
		t.ended[r.Issue.Number] = true
	}
	usd := 0.0
	for _, j := range r.Jobs {
		usd += j.USD
	}
	t.spent[r.Issue.Number] = usd
	// Summed in issue order, so the same run always reaches the same total.
	total := 0.0
	for _, n := range slices.Sorted(maps.Keys(t.spent)) {
		total += t.spent[n]
	}
	stop, over := RunCapped(len(t.ended), total, t.limits)
	if over && t.crossed == "" {
		t.crossed = key
	}
	var reached *RunStop
	if over {
		reached = &stop
	}
	return reached, forPerson(ev) || (over && (ends || key == t.crossed))
}
