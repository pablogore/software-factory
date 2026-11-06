package domain

import "time"

// InitiativeID identifies an initiative: a large ticket worked as a row of
// slices, each one its own job, with a replan after every merge.
type InitiativeID string

// Checkpoint says whether a person approves each slice before it starts.
type Checkpoint string

// Checkpoints an initiative can have. An empty one is the factory's setting
// today, every-slice, so an unset initiative never starts a slice unasked.
const (
	CheckpointEverySlice Checkpoint = "every-slice"
	CheckpointAuto       Checkpoint = "auto"
)

// Stages of an initiative: a slice job running, the replan after a slice
// merged, and the wait for a person's OK before the next slice.
const (
	InitiativeActive     = "active"
	InitiativeReplan     = "replan"
	InitiativeCheckpoint = "checkpoint"
)

// Initiative is the state decide keeps for an initiative. Job is the slice
// job it last heard of: the one whose merge is being replanned or waits at
// the checkpoint.
type Initiative struct {
	ID         InitiativeID `json:"id"`
	Checkpoint Checkpoint   `json:"checkpoint,omitempty"`
	Stage      string       `json:"stage"`
	Job        JobID        `json:"job,omitempty"`
}

// ReplanStatus is what the replan decided after a slice merged.
type ReplanStatus string

// Replan statuses, as in the current factory's replan.md.
const (
	ReplanContinue   ReplanStatus = "CONTINUE"
	ReplanDone       ReplanStatus = "DONE"
	ReplanNeedsHuman ReplanStatus = "NEEDS-HUMAN"
)

// ReplanRequested records that Job, a slice of Initiative, merged and the
// initiative must be replanned before anything else starts.
type ReplanRequested struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (ReplanRequested) EventName() string { return "ReplanRequested" }

// JobID implements Event.
func (e ReplanRequested) JobID() JobID { return e.Job }

// ReplanSubmitted records the replan's outcome after Job merged. It is its
// own event, not a role result, so no new kind or schema is needed.
type ReplanSubmitted struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	Status     ReplanStatus `json:"status"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (ReplanSubmitted) EventName() string { return "ReplanSubmitted" }

// JobID implements Event.
func (e ReplanSubmitted) JobID() JobID { return e.Job }

// CheckpointReached records that the initiative waits for a person's OK
// before its next slice.
type CheckpointReached struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (CheckpointReached) EventName() string { return "CheckpointReached" }

// JobID implements Event.
func (e CheckpointReached) JobID() JobID { return e.Job }

// NextSliceRequested asks to start the initiative's next slice after Job.
// Whether a scheduler slot is free is not decide's concern.
type NextSliceRequested struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (NextSliceRequested) EventName() string { return "NextSliceRequested" }

// JobID implements Event.
func (e NextSliceRequested) JobID() JobID { return e.Job }
