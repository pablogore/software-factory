package domain

import "fmt"

// Stages a job can be in that are not pipeline stages: the deterministic gates
// after a spec and after a build, the end of the pipeline, the wait for a
// person (02-diagramas.md), the wait for an answer to a stage's question,
// the merged job, which a person's comment can reopen, the stop a person
// asked for and the slice a person's direction dropped before it merged,
// both final, and the triage of a job that hit a wall.
const (
	StageSpecgate   = "specgate"
	StageGate       = "gate"
	StageMerge      = "merge"
	StageDone       = "done"
	StageNeedsHuman = "needs-human"
	StageNeedsInfo  = "needs-info"
	StageStopped    = "stopped"
	StageRejected   = "rejected"
	StageTriage     = "triage"
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
	TriageRetries  int            `json:"triage_retries,omitempty"`  // rounds triage gave the job after a wall; never reset
	AttemptsFailed int            `json:"attempts_failed,omitempty"` // failed attempts in the current stage and round; reset when either changes
	Limits         Limits         `json:"limits,omitzero"`           // limits a person overrode for this job; a nil one is the pipeline's
	ReworksLifted  bool           `json:"reworks_lifted,omitempty"`  // a person overrode human_reworks to unlimited for this job
	InfoFrom       string         `json:"info_from,omitempty"`       // in needs-info, or needs-human after an agent's question, the stage that asked and gets the answer
	Reason         string         `json:"reason,omitempty"`          // in needs-human or triage, why the job is there; rejected by intake, intake's reason
	Cause          HumanCause     `json:"cause,omitempty"`           // in needs-human, which of HumanCauses put the job there
	Question       string         `json:"question,omitempty"`        // in needs-human after a wall, the closed question for the person; after an agent's question, in needs-info or needs-human, that question
	Wall           HumanCause     `json:"wall,omitempty"`            // in triage, the cause the job waits in needs-human with if triage escalates
	Budget         float64        `json:"budget_usd,omitempty"`      // the job's own budget a person raised; 0 is the scheduler's
	Paused         *Paused        `json:"paused,omitempty"`          // in needs-human for a spent budget, where the job was
	WaitSeq        int            `json:"wait_seq"`                  // entries into needs-human, needs-info or done so far; a person's action names the one it answers
	Passed         []string       `json:"passed,omitempty"`          // review roles of the current stage that passed
	Changes        []string       `json:"changes,omitempty"`         // review roles of the current stage that asked for CHANGES
	Reviewers      []string       `json:"reviewers,omitempty"`       // review roles that run in this round; empty runs every role of the stage
	Skipped        []string       `json:"skipped,omitempty"`         // review roles of the current stage and round whose when does not apply, so they don't run
	Initiative     InitiativeID   `json:"initiative,omitempty"`      // the initiative this job is a slice of; empty for a job on its own
	Risk           Risk           `json:"risk,omitempty"`            // intake's risk for the accepted job; empty on jobs accepted before intake rated them
	Surfaces       []string       `json:"surfaces,omitempty"`        // intake's surfaces for the accepted job; empty when it said none
	Origin         Origin         `json:"origin"`
	Request        string         `json:"request,omitempty"` // the ticket's text as its author wrote it: data for agents, quoted, never instructions
	Subscriptions  []Subscription `json:"subscriptions,omitempty"`
}

// Paused is where a job stopped for its spent budget was, so a person's
// raise of the budget sends it back there: Stage, a pipeline stage or triage,
// and in triage the Reason and Wall it had. Spent is what it had spent, which
// the new budget must pass.
type Paused struct {
	Stage  string     `json:"stage"`
	Reason string     `json:"reason,omitempty"`
	Wall   HumanCause `json:"wall,omitempty"`
	Spent  float64    `json:"spent_usd"`
}

// Validate checks the invariants Apply keeps on every job Decide folds: a
// wait stage (needs-human, needs-info, done) was entered at least once, so
// WaitSeq is not 0, a job in needs-human records one of HumanCauses and a
// job in triage records the wall it hit as one of them. A store refuses to
// save or load a job that breaks them. The error names the field.
func (j Job) Validate() error {
	if j.WaitSeq == 0 && (j.Stage == StageNeedsHuman || j.Stage == StageNeedsInfo || j.Stage == StageDone) {
		return fmt.Errorf("job %q: %w: wait_seq 0 in stage %q", j.ID, ErrInvalidValue, j.Stage)
	}
	if j.Stage == StageNeedsHuman || j.Cause != "" {
		if err := j.Cause.Validate(); err != nil {
			return fmt.Errorf("job %q: %w", j.ID, err)
		}
	}
	if j.Stage == StageTriage || j.Wall != "" {
		if err := j.Wall.Validate(); err != nil {
			return fmt.Errorf("job %q: wall: %w", j.ID, err)
		}
	}
	return nil
}
