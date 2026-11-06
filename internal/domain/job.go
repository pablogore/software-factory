package domain

// Stages a job can be in that are not pipeline stages: the deterministic gates
// after a spec and after a build, the end of the pipeline and the wait for a
// person (02-diagramas.md).
const (
	StageSpecgate   = "specgate"
	StageGate       = "gate"
	StageMerge      = "merge"
	StageNeedsHuman = "needs-human"
)

// JobID identifies a job inside factoryd.
type JobID string

// Job is one unit of work, from ticket to merge. It is internal to factoryd:
// the tracker issue and the PR are mirrors of it.
type Job struct {
	ID            JobID          `json:"id"`
	Title         string         `json:"title"`
	Pipeline      string         `json:"pipeline"`
	Stage         string         `json:"stage"`
	Round         int            `json:"round"`               // review round, from 1
	Passed        []string       `json:"passed,omitempty"`    // review roles of the current stage that passed
	Changes       []string       `json:"changes,omitempty"`   // review roles of the current stage that asked for CHANGES
	Reviewers     []string       `json:"reviewers,omitempty"` // review roles that run in this round; empty runs every role of the stage
	Origin        Origin         `json:"origin"`
	Subscriptions []Subscription `json:"subscriptions,omitempty"`
}
