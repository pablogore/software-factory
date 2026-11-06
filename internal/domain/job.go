package domain

// Stages a job can be in that are not pipeline stages: the deterministic gates
// after a spec and after a build, the end of the pipeline, the wait for a
// person (02-diagramas.md), the wait for an answer to a stage's question,
// the merged job, which a person's comment can reopen, the stop a person
// asked for and the slice a person's direction dropped before it merged,
// both final.
const (
	StageSpecgate   = "specgate"
	StageGate       = "gate"
	StageMerge      = "merge"
	StageDone       = "done"
	StageNeedsHuman = "needs-human"
	StageNeedsInfo  = "needs-info"
	StageStopped    = "stopped"
	StageRejected   = "rejected"
)

// JobID identifies a job inside factoryd.
type JobID string

// Job is one unit of work, from ticket to merge. It is internal to factoryd:
// the tracker issue and the PR are mirrors of it.
type Job struct {
	ID             JobID          `json:"id"`
	Title          string         `json:"title"`
	Pipeline       string         `json:"pipeline"`
	Stage          string         `json:"stage"`
	Round          int            `json:"round"`                     // review round, from 1
	SpecRejections int            `json:"spec_rejections,omitempty"` // times specgate rejected the spec
	HumanReworks   int            `json:"human_reworks,omitempty"`   // reworks back to build a person sent from needs-human; never reset
	Limits         Limits         `json:"limits,omitzero"`           // limits a person overrode for this job; a nil one is the pipeline's
	ReworksLifted  bool           `json:"reworks_lifted,omitempty"`  // a person overrode human_reworks to unlimited for this job
	InfoFrom       string         `json:"info_from,omitempty"`       // in needs-info, the stage that asked and gets the answer
	Reason         string         `json:"reason,omitempty"`          // in needs-human, why the job is there; rejected by intake, intake's reason
	Cause          HumanCause     `json:"cause,omitempty"`           // in needs-human, which of HumanCauses put the job there
	WaitSeq        int            `json:"wait_seq"`                  // entries into needs-human, needs-info or done so far; a person's action names the one it answers
	Passed         []string       `json:"passed,omitempty"`          // review roles of the current stage that passed
	Changes        []string       `json:"changes,omitempty"`         // review roles of the current stage that asked for CHANGES
	Reviewers      []string       `json:"reviewers,omitempty"`       // review roles that run in this round; empty runs every role of the stage
	Initiative     InitiativeID   `json:"initiative,omitempty"`      // the initiative this job is a slice of; empty for a job on its own
	Origin         Origin         `json:"origin"`
	Subscriptions  []Subscription `json:"subscriptions,omitempty"`
}
