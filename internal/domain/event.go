package domain

import "time"

// Event is a domain fact. decide.Decide consumes one and returns the ones
// that follow; slices persist them in the event log and publish them on the
// bus (07-arquitectura.md). Time always travels inside the event.
type Event interface {
	EventName() string
	// JobID is the job the event is addressed to.
	JobID() JobID
}

// ResultSubmitted records that a role handed back a validated result.
type ResultSubmitted struct {
	Job    JobID     `json:"job"`
	Role   string    `json:"role"`
	Result Result    `json:"result"`
	At     time.Time `json:"at"`
}

// EventName implements Event.
func (ResultSubmitted) EventName() string { return "ResultSubmitted" }

// JobID implements Event.
func (e ResultSubmitted) JobID() JobID { return e.Job }

// HumanApproved records that an allowed person approved the job.
type HumanApproved struct {
	Job JobID     `json:"job"`
	By  string    `json:"by"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (HumanApproved) EventName() string { return "HumanApproved" }

// JobID implements Event.
func (e HumanApproved) JobID() JobID { return e.Job }

// SpecgatePassed records that the deterministic specgate accepted the spec.
type SpecgatePassed struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (SpecgatePassed) EventName() string { return "SpecgatePassed" }

// JobID implements Event.
func (e SpecgatePassed) JobID() JobID { return e.Job }

// SpecgateFailed records that the deterministic specgate rejected the spec.
// Exception, set by specgate, says the spec is over the normal length but
// gives a reason within the exception limits, so a person decides instead of
// the spec being rewritten.
type SpecgateFailed struct {
	Job       JobID     `json:"job"`
	Exception bool      `json:"exception,omitempty"`
	At        time.Time `json:"at"`
}

// EventName implements Event.
func (SpecgateFailed) EventName() string { return "SpecgateFailed" }

// JobID implements Event.
func (e SpecgateFailed) JobID() JobID { return e.Job }

// SpecRejected records the job's count of specgate rejections: one more
// after a rejection, or 0 when a person's feedback starts it again.
type SpecRejected struct {
	Job        JobID     `json:"job"`
	Rejections int       `json:"rejections"`
	At         time.Time `json:"at"`
}

// EventName implements Event.
func (SpecRejected) EventName() string { return "SpecRejected" }

// JobID implements Event.
func (e SpecRejected) JobID() JobID { return e.Job }

// GatePassed records that the deterministic gate (checks) passed on the branch.
type GatePassed struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (GatePassed) EventName() string { return "GatePassed" }

// JobID implements Event.
func (e GatePassed) JobID() JobID { return e.Job }

// GateFailed records that the deterministic gate (checks) failed on the branch.
type GateFailed struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (GateFailed) EventName() string { return "GateFailed" }

// JobID implements Event.
func (e GateFailed) JobID() JobID { return e.Job }

// StageEntered records that the job moved to a pipeline stage or to one of
// the gates (StageSpecgate, StageGate).
type StageEntered struct {
	Job   JobID     `json:"job"`
	Stage string    `json:"stage"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (StageEntered) EventName() string { return "StageEntered" }

// JobID implements Event.
func (e StageEntered) JobID() JobID { return e.Job }

// ReviewPassed records that one review role of the current stage passed.
type ReviewPassed struct {
	Job  JobID     `json:"job"`
	Role string    `json:"role"`
	At   time.Time `json:"at"`
}

// EventName implements Event.
func (ReviewPassed) EventName() string { return "ReviewPassed" }

// JobID implements Event.
func (e ReviewPassed) JobID() JobID { return e.Job }

// ChangesRequested records that one review role of the current stage asked
// for CHANGES.
type ChangesRequested struct {
	Job      JobID     `json:"job"`
	Role     string    `json:"role"`
	Findings []Finding `json:"findings,omitempty"`
	At       time.Time `json:"at"`
}

// EventName implements Event.
func (ChangesRequested) EventName() string { return "ChangesRequested" }

// JobID implements Event.
func (e ChangesRequested) JobID() JobID { return e.Job }

// RoundStarted records that the job starts another review round after CHANGES.
// Reviewers are the review roles that run in it; the others keep their pass.
type RoundStarted struct {
	Job       JobID     `json:"job"`
	Round     int       `json:"round"`
	Reviewers []string  `json:"reviewers,omitempty"`
	At        time.Time `json:"at"`
}

// EventName implements Event.
func (RoundStarted) EventName() string { return "RoundStarted" }

// JobID implements Event.
func (e RoundStarted) JobID() JobID { return e.Job }

// NeedsHuman records that the job stops until a person acts, and why.
// SpecException says it waits on a spec's length exception: a person's OK
// lets the spec through and their feedback sends it back to spec.
// SpecEscalated says specgate rejected the spec too often: a person's feedback
// goes to a rewritten spec, whatever the rejection count says by then.
type NeedsHuman struct {
	Job           JobID     `json:"job"`
	Reason        string    `json:"reason"`
	SpecException bool      `json:"spec_exception,omitempty"`
	SpecEscalated bool      `json:"spec_escalated,omitempty"`
	At            time.Time `json:"at"`
}

// EventName implements Event.
func (NeedsHuman) EventName() string { return "NeedsHuman" }

// JobID implements Event.
func (e NeedsHuman) JobID() JobID { return e.Job }

// NeedsInfo records that the job waits for a person to answer a question
// from stage From, which gets the answer.
type NeedsInfo struct {
	Job  JobID     `json:"job"`
	From string    `json:"from"`
	At   time.Time `json:"at"`
}

// EventName implements Event.
func (NeedsInfo) EventName() string { return "NeedsInfo" }

// JobID implements Event.
func (e NeedsInfo) JobID() JobID { return e.Job }

// MergeRequested records that the job finished its pipeline and asks to merge.
type MergeRequested struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (MergeRequested) EventName() string { return "MergeRequested" }

// JobID implements Event.
func (e MergeRequested) JobID() JobID { return e.Job }

// MergeCompleted records that the merge the job asked for went through.
type MergeCompleted struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (MergeCompleted) EventName() string { return "MergeCompleted" }

// JobID implements Event.
func (e MergeCompleted) JobID() JobID { return e.Job }

// AuthorKind says who wrote a message: a person, a bot or CI.
type AuthorKind string

// Author kinds the event sources tell apart.
const (
	AuthorPerson AuthorKind = "person"
	AuthorBot    AuthorKind = "bot"
	AuthorCI     AuthorKind = "ci"
)

// MessageReceived records a message on one of the job's threads. The event
// source decides Allowed (the provider's allowlist, 03-integraciones.md),
// AuthorKind and Direction, a change of course for the job's initiative;
// decide.Decide trusts all three and only acts on an allowed person. The text
// alone never makes a message a direction.
type MessageReceived struct {
	Job        JobID      `json:"job"`
	Provider   string     `json:"provider"`
	Thread     string     `json:"thread"`
	Author     string     `json:"author"`
	AuthorKind AuthorKind `json:"author_kind"`
	Allowed    bool       `json:"allowed"`
	Direction  bool       `json:"direction"`
	Text       string     `json:"text"`
	At         time.Time  `json:"at"`
}

// EventName implements Event.
func (MessageReceived) EventName() string { return "MessageReceived" }

// JobID implements Event.
func (e MessageReceived) JobID() JobID { return e.Job }

// ReworkRequested records that a person sent the job back to build, or to
// spec after specgate's last rejection or a length exception, with feedback;
// that stage gets Text as input.
type ReworkRequested struct {
	Job  JobID     `json:"job"`
	By   string    `json:"by"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// EventName implements Event.
func (ReworkRequested) EventName() string { return "ReworkRequested" }

// JobID implements Event.
func (e ReworkRequested) JobID() JobID { return e.Job }

// InfoAnswered records that a person answered the job's question; the stage
// that asked gets Text as input.
type InfoAnswered struct {
	Job  JobID     `json:"job"`
	By   string    `json:"by"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// EventName implements Event.
func (InfoAnswered) EventName() string { return "InfoAnswered" }

// JobID implements Event.
func (e InfoAnswered) JobID() JobID { return e.Job }

// StopRequested asks to stop the job for good. Like HumanApproved, the event
// source vouches that an allowed person sent it; By names that person.
type StopRequested struct {
	Job JobID     `json:"job"`
	By  string    `json:"by"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (StopRequested) EventName() string { return "StopRequested" }

// JobID implements Event.
func (e StopRequested) JobID() JobID { return e.Job }

// JobStopped records who stopped the job and when; nothing moves it afterwards.
type JobStopped struct {
	Job JobID     `json:"job"`
	By  string    `json:"by"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (JobStopped) EventName() string { return "JobStopped" }

// JobID implements Event.
func (e JobStopped) JobID() JobID { return e.Job }
