package domain

import "fmt"

// Result is what a role hands back through submit_result. There is one result
// type per Kind, with the verdict and payload of the kinds table in 04-roles.md.
type Result interface {
	Kind() Kind
	Validate() error
}

// IntakeDecision is the verdict of an intake role.
type IntakeDecision string

// Intake decisions.
const (
	IntakeAccept    IntakeDecision = "ACCEPT"
	IntakeNeedsInfo IntakeDecision = "NEEDS_INFO"
	IntakeReject    IntakeDecision = "REJECT"
)

// Slice is one piece of work an accepted intake splits the ticket into.
type Slice struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// IntakeResult starts, asks about or discards a ticket. Reason is the intake
// agent's own words, kept on a rejected job; it is optional, so results
// written before it existed still validate.
type IntakeResult struct {
	Decision IntakeDecision `json:"decision"`
	Slices   []Slice        `json:"slices,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}

// Kind implements Result.
func (IntakeResult) Kind() Kind { return KindIntake }

// Validate implements Result.
func (r IntakeResult) Validate() error {
	return oneOf("decision", r.Decision, IntakeAccept, IntakeNeedsInfo, IntakeReject)
}

// SpecResult carries the structured spec that specgate checks next.
type SpecResult struct {
	Title string `json:"title"`
	Spec  string `json:"spec"`
}

// Kind implements Result.
func (SpecResult) Kind() Kind { return KindSpec }

// Validate implements Result.
func (r SpecResult) Validate() error {
	if r.Spec == "" {
		return fmt.Errorf("%w: spec", ErrMissingField)
	}
	return nil
}

// BuildStatus is the verdict of a build role.
type BuildStatus string

// Build statuses.
const (
	BuildReady   BuildStatus = "READY"
	BuildBlocked BuildStatus = "BLOCKED"
)

// BuildResult says whether the branch is ready for the deterministic gate.
type BuildResult struct {
	Status  BuildStatus `json:"status"`
	Summary string      `json:"summary"`
}

// Kind implements Result.
func (BuildResult) Kind() Kind { return KindBuild }

// Validate implements Result.
func (r BuildResult) Validate() error {
	return oneOf("status", r.Status, BuildReady, BuildBlocked)
}

// ReviewVerdict is the verdict of a review role.
type ReviewVerdict string

// Review verdicts.
const (
	ReviewPass    ReviewVerdict = "PASS"
	ReviewChanges ReviewVerdict = "CHANGES"
)

// ReviewResult sends the job back to build or lets it go on.
type ReviewResult struct {
	Verdict  ReviewVerdict `json:"verdict"`
	Findings []Finding     `json:"findings,omitempty"`
}

// Kind implements Result.
func (ReviewResult) Kind() Kind { return KindReview }

// Validate implements Result. A finding error names the finding's index and
// the missing field.
func (r ReviewResult) Validate() error {
	if err := oneOf("verdict", r.Verdict, ReviewPass, ReviewChanges); err != nil {
		return err
	}
	for i, f := range r.Findings {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("findings[%d]: %w", i, err)
		}
	}
	return nil
}

// ApproveDecision is the verdict of an approve role.
type ApproveDecision string

// Approve decisions.
const (
	ApproveApprove  ApproveDecision = "APPROVE"
	ApproveEscalate ApproveDecision = "ESCALATE"
)

// ApproveResult leads to merge or to needs-human.
type ApproveResult struct {
	Decision ApproveDecision `json:"decision"`
	Reasons  []string        `json:"reasons,omitempty"`
}

// Kind implements Result.
func (ApproveResult) Kind() Kind { return KindApprove }

// Validate implements Result.
func (r ApproveResult) Validate() error {
	return oneOf("decision", r.Decision, ApproveApprove, ApproveEscalate)
}

// Check is one `verify` check of a task role and whether it passed.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// TaskResult lists the files a generic task produced (its `produces`) and the
// outcome of its `verify` checks.
type TaskResult struct {
	Produces []string `json:"produces"`
	Verify   []Check  `json:"verify,omitempty"`
}

// Kind implements Result.
func (TaskResult) Kind() Kind { return KindTask }

// Validate implements Result.
func (TaskResult) Validate() error { return nil }

// TriageDecision is the verdict of a triage role.
type TriageDecision string

// Triage decisions.
const (
	TriageRetry    TriageDecision = "RETRY"
	TriageEscalate TriageDecision = "ESCALATE"
)

// TriageResult gives a job that hit a wall one more round, with Brief as the
// builder's instructions, or sends it to a person with Question, one closed
// question (yes/no or a few options) they can answer in a line.
type TriageResult struct {
	Decision TriageDecision `json:"decision"`
	Brief    string         `json:"brief,omitempty"`
	Question string         `json:"question,omitempty"`
}

// Kind implements Result.
func (TriageResult) Kind() Kind { return KindTriage }

// Validate implements Result. A RETRY needs its brief and an ESCALATE its
// question, since each is all the next one to act gets.
func (r TriageResult) Validate() error {
	if err := oneOf("decision", r.Decision, TriageRetry, TriageEscalate); err != nil {
		return err
	}
	if r.Decision == TriageRetry && Blank(r.Brief) {
		return fmt.Errorf("%w: brief", ErrMissingField)
	}
	if r.Decision == TriageEscalate && Blank(r.Question) {
		return fmt.Errorf("%w: question", ErrMissingField)
	}
	return nil
}

func oneOf[T ~string](field string, v T, allowed ...T) error {
	if v == "" {
		return fmt.Errorf("%w: %s", ErrMissingField, field)
	}
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %q", ErrInvalidValue, field, v)
}
