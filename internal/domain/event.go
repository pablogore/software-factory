package domain

import "time"

// Event is a domain fact. decide.Decide consumes one and returns the ones
// that follow; slices persist them in the event log and publish them on the
// bus (07-arquitectura.md). Time always travels inside the event.
type Event interface {
	EventName() string
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

// HumanApproved records that an allowed person approved the job.
type HumanApproved struct {
	Job JobID     `json:"job"`
	By  string    `json:"by"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (HumanApproved) EventName() string { return "HumanApproved" }

// SpecgatePassed records that the deterministic specgate accepted the spec.
type SpecgatePassed struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (SpecgatePassed) EventName() string { return "SpecgatePassed" }

// GatePassed records that the deterministic gate (checks) passed on the branch.
type GatePassed struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (GatePassed) EventName() string { return "GatePassed" }

// StageEntered records that the job moved to a pipeline stage or to one of
// the gates (StageSpecgate, StageGate).
type StageEntered struct {
	Job   JobID     `json:"job"`
	Stage string    `json:"stage"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (StageEntered) EventName() string { return "StageEntered" }

// ReviewPassed records that one review role of the current stage passed.
type ReviewPassed struct {
	Job  JobID     `json:"job"`
	Role string    `json:"role"`
	At   time.Time `json:"at"`
}

// EventName implements Event.
func (ReviewPassed) EventName() string { return "ReviewPassed" }

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

// RoundStarted records that the job starts another review round after CHANGES.
type RoundStarted struct {
	Job   JobID     `json:"job"`
	Round int       `json:"round"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (RoundStarted) EventName() string { return "RoundStarted" }

// NeedsHuman records that the job stops until a person acts, and why.
type NeedsHuman struct {
	Job    JobID     `json:"job"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// EventName implements Event.
func (NeedsHuman) EventName() string { return "NeedsHuman" }

// MergeRequested records that the job finished its pipeline and asks to merge.
type MergeRequested struct {
	Job JobID     `json:"job"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (MergeRequested) EventName() string { return "MergeRequested" }

// AuthorKind says who wrote a message: a person, a bot or CI.
type AuthorKind string

// Author kinds the event sources tell apart.
const (
	AuthorPerson AuthorKind = "person"
	AuthorBot    AuthorKind = "bot"
	AuthorCI     AuthorKind = "ci"
)

// MessageReceived records a message on one of the job's threads. The event
// source decides Allowed (the provider's allowlist, 03-integraciones.md) and
// AuthorKind; decide.Decide trusts both and only acts on an allowed person.
type MessageReceived struct {
	Job        JobID      `json:"job"`
	Provider   string     `json:"provider"`
	Thread     string     `json:"thread"`
	Author     string     `json:"author"`
	AuthorKind AuthorKind `json:"author_kind"`
	Allowed    bool       `json:"allowed"`
	Text       string     `json:"text"`
	At         time.Time  `json:"at"`
}

// EventName implements Event.
func (MessageReceived) EventName() string { return "MessageReceived" }

// ReworkRequested records that a person sent the job back to build with
// feedback; the build stage gets Text as input.
type ReworkRequested struct {
	Job  JobID     `json:"job"`
	By   string    `json:"by"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// EventName implements Event.
func (ReworkRequested) EventName() string { return "ReworkRequested" }
