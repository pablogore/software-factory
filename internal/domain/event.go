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
