package domain

// HumanCause is why a job waits in needs-human. It is a closed set set by the
// event that put the job there, never read from Reason, which is free text
// for a person: Decide may route on the cause, never on the text.
type HumanCause string

// The six causes, one per way into needs-human.
const (
	CauseBlocked       HumanCause = "blocked"        // the builder reported BLOCKED
	CauseEscalated     HumanCause = "escalated"      // the approver said ESCALATE
	CauseChanges       HumanCause = "changes"        // a blocking reviewer asked for CHANGES in the last round
	CauseGateFailed    HumanCause = "gate-failed"    // the gate failed in the last round
	CauseSpecEscalated HumanCause = "spec-escalated" // specgate rejected the spec too often
	CauseSpecException HumanCause = "spec-exception" // the spec asks for a length exception
)

// HumanCauses lists every cause.
var HumanCauses = []HumanCause{CauseBlocked, CauseEscalated, CauseChanges, CauseGateFailed, CauseSpecEscalated, CauseSpecException}

// Validate returns an error when c is not one of HumanCauses.
func (c HumanCause) Validate() error {
	return oneOf("cause", c, HumanCauses...)
}
