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

// HumanApproved records that an allowed person approved the job. WaitSeq is
// the job's WaitSeq when the OK was given: it acts only on that wait.
type HumanApproved struct {
	Job     JobID     `json:"job"`
	By      string    `json:"by"`
	WaitSeq int       `json:"wait_seq"`
	At      time.Time `json:"at"`
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

// GatePassed records that the deterministic gate (checks) passed on the
// branch; Run is the checks run it judged.
type GatePassed struct {
	Job JobID     `json:"job"`
	Run ChecksRun `json:"run"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (GatePassed) EventName() string { return "GatePassed" }

// JobID implements Event.
func (e GatePassed) JobID() JobID { return e.Job }

// GateFailed records that the deterministic gate (checks) failed on the
// branch; Run is the checks run it judged.
type GateFailed struct {
	Job JobID     `json:"job"`
	Run ChecksRun `json:"run"`
	At  time.Time `json:"at"`
}

// EventName implements Event.
func (GateFailed) EventName() string { return "GateFailed" }

// JobID implements Event.
func (e GateFailed) JobID() JobID { return e.Job }

// ChecksRan keeps with the job the checks run the gate judged in Round, so
// the builder of the next round reads its log and a person sees why.
type ChecksRan struct {
	Job   JobID     `json:"job"`
	Round int       `json:"round"`
	Run   ChecksRun `json:"run"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (ChecksRan) EventName() string { return "ChecksRan" }

// JobID implements Event.
func (e ChecksRan) JobID() JobID { return e.Job }

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

// NeedsHuman records that the job stops until a person acts, and why: Cause
// is one of HumanCauses, and what a person's OK or feedback does is decided
// from it alone; Reason is the same cause in words for a person. After a
// wall, Question is the closed question the person answers, data only. For
// a spent budget, Spent is what the job had spent, in USD. After an agent's
// question, Question is that question and From the stage that asked, which
// gets the answer.
type NeedsHuman struct {
	Job      JobID      `json:"job"`
	Reason   string     `json:"reason"`
	Question string     `json:"question,omitempty"`
	From     string     `json:"from,omitempty"`
	Cause    HumanCause `json:"cause"`
	Spent    float64    `json:"spent_usd,omitempty"`
	At       time.Time  `json:"at"`
}

// EventName implements Event.
func (NeedsHuman) EventName() string { return "NeedsHuman" }

// JobID implements Event.
func (e NeedsHuman) JobID() JobID { return e.Job }

// TriageRequested records that the job hit a wall (a builder BLOCKED, a gate
// failed or CHANGES in the last round, a failed merge) with triage retries
// left, so triage looks at it before a person does. Cause is the one the job
// waits in needs-human with if triage escalates, and Reason says it in words.
type TriageRequested struct {
	Job    JobID      `json:"job"`
	Reason string     `json:"reason"`
	Cause  HumanCause `json:"cause"`
	At     time.Time  `json:"at"`
}

// EventName implements Event.
func (TriageRequested) EventName() string { return "TriageRequested" }

// JobID implements Event.
func (e TriageRequested) JobID() JobID { return e.Job }

// TriageRetried records that triage gave the job one more round; the build
// stage gets Brief, Role's instructions, as input. Brief is agent-written
// text, data only.
type TriageRetried struct {
	Job   JobID     `json:"job"`
	Role  string    `json:"role"`
	Brief string    `json:"brief"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (TriageRetried) EventName() string { return "TriageRetried" }

// JobID implements Event.
func (e TriageRetried) JobID() JobID { return e.Job }

// MergeFailed records that the merge the job asked for did not go through,
// such as a conflict with the base or a gate failing after syncing. Reason
// is the merge's own answer, data only: Decide never parses it or routes on it.
type MergeFailed struct {
	Job    JobID     `json:"job"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// EventName implements Event.
func (MergeFailed) EventName() string { return "MergeFailed" }

// JobID implements Event.
func (e MergeFailed) JobID() JobID { return e.Job }

// BudgetExhausted records that the job was about to launch an agent with
// its whole budget already spent, so the agent never ran. Spent is what the
// job had spent, in USD: a person's raise of its budget must pass it.
type BudgetExhausted struct {
	Job   JobID     `json:"job"`
	Spent float64   `json:"spent_usd"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (BudgetExhausted) EventName() string { return "BudgetExhausted" }

// JobID implements Event.
func (e BudgetExhausted) JobID() JobID { return e.Job }

// ActionFailed records that the destination refused one of the job's
// external actions for permission or validation, so trying it again would
// fail the same way. Key and Kind name the action and Error is the
// destination's answer, data only: Decide never parses it or routes on it.
type ActionFailed struct {
	Job   JobID     `json:"job"`
	Key   string    `json:"key"`
	Kind  string    `json:"kind"`
	Error string    `json:"error"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (ActionFailed) EventName() string { return "ActionFailed" }

// JobID implements Event.
func (e ActionFailed) JobID() JobID { return e.Job }

// AttemptFailed records that Role's session on the job ended its turn
// without a saved result, and again after factoryd asked it once more and
// said why. Reason is why the second turn has none, data only: Decide never
// parses it or routes on it. It changes no state.
type AttemptFailed struct {
	Job    JobID     `json:"job"`
	Role   string    `json:"role"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// EventName implements Event.
func (AttemptFailed) EventName() string { return "AttemptFailed" }

// JobID implements Event.
func (e AttemptFailed) JobID() JobID { return e.Job }

// NeedsInfo records that the job waits for a person to answer a question
// from stage From, which gets the answer. Question is the question an
// agent asked with ask_human, empty when intake asked for more information.
type NeedsInfo struct {
	Job      JobID     `json:"job"`
	From     string    `json:"from"`
	Question string    `json:"question,omitempty"`
	At       time.Time `json:"at"`
}

// EventName implements Event.
func (NeedsInfo) EventName() string { return "NeedsInfo" }

// JobID implements Event.
func (e NeedsInfo) JobID() JobID { return e.Job }

// JobRejected records that intake discarded the ticket: the job ends
// rejected, final, with Reason. On a slice job it comes with a
// ReplanRequested. Reason is agent-written text, data only:
// adapters show it quoted and escaped, never as markup or commands.
type JobRejected struct {
	Job    JobID     `json:"job"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// EventName implements Event.
func (JobRejected) EventName() string { return "JobRejected" }

// JobID implements Event.
func (e JobRejected) JobID() JobID { return e.Job }

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
// alone never makes a message a direction. WaitSeq is the job's WaitSeq when
// the message was sent: it acts only on that wait.
type MessageReceived struct {
	Job        JobID      `json:"job"`
	Provider   string     `json:"provider"`
	Thread     string     `json:"thread"`
	Author     string     `json:"author"`
	AuthorKind AuthorKind `json:"author_kind"`
	Allowed    bool       `json:"allowed"`
	Direction  bool       `json:"direction"`
	Text       string     `json:"text"`
	WaitSeq    int        `json:"wait_seq"`
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

// OverrideRequested asks to merge a job that waits in needs-human after
// CHANGES in the last round: the only way a person merges work review
// rejected. Like MessageReceived, the event source decides Allowed and
// AuthorKind, and decide.Decide only acts on an allowed person; By names
// that person. Reason is their own words, data only: Decide never parses it
// or routes on it. WaitSeq is the job's WaitSeq when the override was asked:
// it acts only on that wait.
type OverrideRequested struct {
	Job        JobID      `json:"job"`
	By         string     `json:"by"`
	AuthorKind AuthorKind `json:"author_kind"`
	Allowed    bool       `json:"allowed"`
	Reason     string     `json:"reason"`
	WaitSeq    int        `json:"wait_seq"`
	At         time.Time  `json:"at"`
}

// EventName implements Event.
func (OverrideRequested) EventName() string { return "OverrideRequested" }

// JobID implements Event.
func (e OverrideRequested) JobID() JobID { return e.Job }

// MergeOverridden is the job's history entry for an accepted override: who
// merged work review rejected, why and when. A MergeRequested follows it.
type MergeOverridden struct {
	Job    JobID     `json:"job"`
	By     string    `json:"by"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// EventName implements Event.
func (MergeOverridden) EventName() string { return "MergeOverridden" }

// JobID implements Event.
func (e MergeOverridden) JobID() JobID { return e.Job }

// LimitOverrideRequested asks to change one retry limit for this job only,
// such as one more human rework, without changing the pipeline. Like
// OverrideRequested, the event source decides Allowed and AuthorKind, and
// decide.Decide only acts on an allowed person; By names that person. Limit
// is one of LimitKeys and Value is the new value as written ("4" or
// "unlimited"). WaitSeq is the job's WaitSeq when it was asked: it acts only
// on that needs-human wait.
type LimitOverrideRequested struct {
	Job        JobID      `json:"job"`
	By         string     `json:"by"`
	AuthorKind AuthorKind `json:"author_kind"`
	Allowed    bool       `json:"allowed"`
	Limit      string     `json:"limit"`
	Value      string     `json:"value"`
	WaitSeq    int        `json:"wait_seq"`
	At         time.Time  `json:"at"`
}

// EventName implements Event.
func (LimitOverrideRequested) EventName() string { return "LimitOverrideRequested" }

// JobID implements Event.
func (e LimitOverrideRequested) JobID() JobID { return e.Job }

// LimitOverridden is the job's history entry for an accepted limit override:
// who changed which limit to what, and when. Value is the new limit, or 0
// with Unlimited for human_reworks lifted. It answers the wait it was asked
// for, so the job waits in needs-human again under a new WaitSeq.
type LimitOverridden struct {
	Job       JobID     `json:"job"`
	By        string    `json:"by"`
	Limit     string    `json:"limit"`
	Value     int       `json:"value,omitempty"`
	Unlimited bool      `json:"unlimited,omitempty"`
	At        time.Time `json:"at"`
}

// EventName implements Event.
func (LimitOverridden) EventName() string { return "LimitOverridden" }

// JobID implements Event.
func (e LimitOverridden) JobID() JobID { return e.Job }

// CapRaiseRequested asks to raise one cap of the job's issue: its slices or
// its USD, so an issue stopped at that cap starts slices again, or the job's
// own budget, so a job stopped for its spent budget goes back to the stage it
// was in. It is the only way either resumes. Like LimitOverrideRequested, the
// event source decides Allowed and AuthorKind, and only an allowed person's
// raise acts; By names that person. Cap is one of IssueCaps and Value the new
// cap, which must pass the value reached. WaitSeq is the wait it answers: the
// job's for its budget, the issue's stop for its slices or USD.
type CapRaiseRequested struct {
	Job        JobID      `json:"job"`
	By         string     `json:"by"`
	AuthorKind AuthorKind `json:"author_kind"`
	Allowed    bool       `json:"allowed"`
	Cap        IssueCap   `json:"cap"`
	Value      float64    `json:"value"`
	WaitSeq    int        `json:"wait_seq"`
	At         time.Time  `json:"at"`
}

// EventName implements Event.
func (CapRaiseRequested) EventName() string { return "CapRaiseRequested" }

// JobID implements Event.
func (e CapRaiseRequested) JobID() JobID { return e.Job }

// CapRaised is the job's history entry for an accepted raise of its budget:
// who raised it to what, and when. The event that resumes the job follows it.
type CapRaised struct {
	Job   JobID     `json:"job"`
	By    string    `json:"by"`
	Cap   IssueCap  `json:"cap"`
	Value float64   `json:"value"`
	At    time.Time `json:"at"`
}

// EventName implements Event.
func (CapRaised) EventName() string { return "CapRaised" }

// JobID implements Event.
func (e CapRaised) JobID() JobID { return e.Job }

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
