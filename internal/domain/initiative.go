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
// merged, the wait for a person's OK before the next slice, closed for good
// after a replan DONE, and stopped for a person after a replan NEEDS-HUMAN
// until that person's answer sends it back to the replan.
const (
	InitiativeActive     = "active"
	InitiativeReplan     = "replan"
	InitiativeCheckpoint = "checkpoint"
	InitiativeClosed     = "closed"
	InitiativeNeedsHuman = "needs-human"
)

// Initiative is the state decide keeps for an initiative. Job is the slice
// job it last heard of: the one running while it is active, or whose merge
// is being replanned or waits at the checkpoint; it is empty from the request
// for the next slice until that slice starts. Reason is why it needs a
// person, as the replan wrote it.
type Initiative struct {
	ID         InitiativeID `json:"id"`
	Checkpoint Checkpoint   `json:"checkpoint,omitempty"`
	Stage      string       `json:"stage"`
	Job        JobID        `json:"job,omitempty"`
	Reason     string       `json:"reason,omitempty"`
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

// SliceDropped records that By's direction dropped Job, a slice of
// Initiative that never merged: the job ends rejected and the replan gets
// Text, the new direction.
type SliceDropped struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	By         string       `json:"by"`
	Text       string       `json:"text"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (SliceDropped) EventName() string { return "SliceDropped" }

// JobID implements Event.
func (e SliceDropped) JobID() JobID { return e.Job }

// ReplanSubmitted records the replan's outcome after Job merged. It is its
// own event, not a role result, so no new kind or schema is needed. Reason
// is the replan agent's own words, kept when it asks for a person.
type ReplanSubmitted struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	Status     ReplanStatus `json:"status"`
	Reason     string       `json:"reason,omitempty"`
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

// SliceStarted records that Job started as the initiative's next slice, after
// a NextSliceRequested. From then on only Job's merge or drop replans it.
type SliceStarted struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (SliceStarted) EventName() string { return "SliceStarted" }

// JobID implements Event.
func (e SliceStarted) JobID() JobID { return e.Job }

// InitiativeEnded records that the replan after Job said DONE: the
// initiative is closed and no slice follows.
type InitiativeEnded struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (InitiativeEnded) EventName() string { return "InitiativeEnded" }

// JobID implements Event.
func (e InitiativeEnded) JobID() JobID { return e.Job }

// InitiativeNeedsHumanRaised records that the replan after Job said NEEDS-HUMAN:
// the initiative stops for a person, for the replan's Reason.
type InitiativeNeedsHumanRaised struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	Reason     string       `json:"reason"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (InitiativeNeedsHumanRaised) EventName() string { return "InitiativeNeedsHumanRaised" }

// JobID implements Event.
func (e InitiativeNeedsHumanRaised) JobID() JobID { return e.Job }

// InitiativeAnswered records that By, an allowed person, answered the
// initiative stopped in needs-human after Job's replan: the replan runs again
// with Text as its input.
type InitiativeAnswered struct {
	Job        JobID        `json:"job"`
	Initiative InitiativeID `json:"initiative"`
	By         string       `json:"by"`
	Text       string       `json:"text"`
	At         time.Time    `json:"at"`
}

// EventName implements Event.
func (InitiativeAnswered) EventName() string { return "InitiativeAnswered" }

// JobID implements Event.
func (e InitiativeAnswered) JobID() JobID { return e.Job }
