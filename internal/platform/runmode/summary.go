package runmode

import (
	"slices"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
)

// JobReport is what the summary tells of one job of an issue.
type JobReport struct {
	Job domain.Job
	// PR is the job's pull request; empty when it opened none.
	PR string
	// USD is what the job's agents really cost.
	USD float64
	// Start and End are the times of the job's first and last events.
	Start, End time.Time
}

// IssueReport is what became of an issue: the job on its own, or every
// slice of its initiative, in the order they started.
type IssueReport struct {
	Issue queue.Issue
	Jobs  []JobReport
}

// Option is what the operator may do after the brake.
type Option string

// The options every summary offers.
const (
	OptionContinue Option = "continue"
	OptionPick     Option = "pick"
	OptionReplan   Option = "replan"
	OptionStop     Option = "stop"
)

// Options lists the options in the order a summary offers them.
var Options = []Option{OptionContinue, OptionPick, OptionReplan, OptionStop}

// Slice is one job of the issue as the summary shows it.
type Slice struct {
	Job    domain.JobID `json:"job"`
	Title  string       `json:"title"`
	Stage  string       `json:"stage"`
	Rounds int          `json:"rounds"`
}

// Next is the issue the summary recommends taking next, and why.
type Next struct {
	Issue int    `json:"issue"`
	Title string `json:"title"`
	Why   string `json:"why"`
}

// Summary is what the brake publishes about the issue that ended.
type Summary struct {
	Issue int    `json:"issue"`
	Title string `json:"title"`
	// Stage is the final state of the job that put the brake on.
	Stage    string        `json:"stage"`
	PRs      []string      `json:"prs"`
	Slices   []Slice       `json:"slices"`
	Rounds   int           `json:"rounds"`
	USD      float64       `json:"usd"`
	Duration time.Duration `json:"duration"`
	// Next is nil when no issue waits.
	Next    *Next    `json:"next,omitempty"`
	Options []Option `json:"options"`
}

// Summarize sums up r after its job by ended, and recommends the first of
// waiting, the queue's issues in the order it takes them, by order.
func Summarize(r IssueReport, by domain.JobID, waiting []queue.Issue, order queue.Order) Summary {
	s := Summary{Issue: r.Issue.Number, Title: r.Issue.Title, PRs: []string{}, Slices: []Slice{}, Options: slices.Clone(Options)}
	var start, end time.Time
	for _, j := range r.Jobs {
		if j.Job.ID == by {
			s.Stage = j.Job.Stage
		}
		if j.PR != "" {
			s.PRs = append(s.PRs, j.PR)
		}
		s.Slices = append(s.Slices, Slice{Job: j.Job.ID, Title: j.Job.Title, Stage: j.Job.Stage, Rounds: j.Job.Round})
		s.Rounds += j.Job.Round
		s.USD += j.USD
		if start.IsZero() || j.Start.Before(start) {
			start = j.Start
		}
		if j.End.After(end) {
			end = j.End
		}
	}
	s.Duration = end.Sub(start)
	if len(waiting) > 0 {
		is := waiting[0]
		s.Next = &Next{Issue: is.Number, Title: is.Title, Why: why(is, order)}
	}
	return s
}

// why says in one line what put is first in the queue, by the rule
// queue.Sort follows. The text is for a person, in the factory's language.
func why(is queue.Issue, o queue.Order) string {
	switch {
	case slices.Contains(o.TechLead, is.Number):
		return "es el primero en el orden del tech lead"
	case len(o.TechLead) == 0 && slices.Contains(o.Priority, is.Number):
		return "es el primero en la prioridad del product owner"
	default:
		return "ningún orden lo lista y es el de número más bajo entre los que esperan"
	}
}
