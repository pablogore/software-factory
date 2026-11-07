package domain

// HumanCause is why a job waits in needs-human. It is a closed set set by the
// event that put the job there, never read from Reason, which is free text
// for a person: Decide may route on the cause, never on the text.
type HumanCause string

// The causes, one per way into needs-human.
const (
	CauseBlocked       HumanCause = "blocked"        // the builder reported BLOCKED
	CauseEscalated     HumanCause = "escalated"      // the approver said ESCALATE
	CauseChanges       HumanCause = "changes"        // a blocking reviewer asked for CHANGES in the last round
	CauseGateFailed    HumanCause = "gate-failed"    // the gate failed in the last round
	CauseSpecEscalated HumanCause = "spec-escalated" // specgate rejected the spec too often
	CauseSpecException HumanCause = "spec-exception" // the spec asks for a length exception
	CauseActionFailed  HumanCause = "action-failed"  // the destination refused an external action
	CauseMergeFailed   HumanCause = "merge-failed"   // the merge did not go through
)

// CauseBudget means the job was about to launch an agent with its whole
// budget spent. Resuming it after a person answers is not decided yet.
const CauseBudget HumanCause = "budget-exhausted"

// humanCauses is unexported because what a human OK may merge depends on
// this set: no other package may add a cause to it.
var humanCauses = []HumanCause{CauseBlocked, CauseEscalated, CauseChanges, CauseGateFailed, CauseSpecEscalated, CauseSpecException, CauseActionFailed, CauseMergeFailed, CauseBudget}

// HumanCauses lists every cause. It returns a copy, so changing it changes
// neither the set nor what Validate accepts.
func HumanCauses() []HumanCause {
	return append([]HumanCause(nil), humanCauses...)
}

// Validate returns an error when c is not one of HumanCauses.
func (c HumanCause) Validate() error {
	return oneOf("cause", c, humanCauses...)
}
