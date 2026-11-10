package domain

import "time"

// ProgressKind says what an agent reported in an AgentProgressed.
type ProgressKind string

// What an agent reports while it works, and the end of its turn.
const (
	// ProgressMessage is text the agent wrote, in Text.
	ProgressMessage ProgressKind = "message"
	// ProgressToolCall is a tool call the agent started or updated: ToolCall
	// names it, Text is its title and ToolStatus where it is.
	ProgressToolCall ProgressKind = "tool_call"
	// ProgressUsage is the tokens the agent says the turn used. Agents that
	// do not report it send none.
	ProgressUsage ProgressKind = "usage"
	// ProgressEnded is the end of the turn: StopReason says why the agent
	// stopped, or Error why the turn failed.
	ProgressEnded ProgressKind = "ended"
	// ProgressPermissionDenied is a permission the agent asked for and
	// factoryd denied: ToolCall names the call, Text the tool and Reason why.
	ProgressPermissionDenied ProgressKind = "permission_denied"
)

// MaxProgressText is the most characters a progress message an agent
// reports with the progress tool may have: enough for a few lines of what it
// is doing, short enough for a chat message.
const MaxProgressText = 2000

// Stop reasons of a turn that factoryd ended itself rather than the agent.
const (
	// StopCancelled is a turn of a session that was cancelled. It is ACP's
	// own reason for it, so agents that stop when asked report it too.
	StopCancelled = "cancelled"
	// StopTimedOut is a turn of a session that ran past its time limit.
	StopTimedOut = "timed_out"
)

// AgentProgressed records what an agent working on the job reported during a
// session, for the agents' log and the channels (06-interfaz-y-
// observabilidad.md). It changes no state.
type AgentProgressed struct {
	Job          JobID        `json:"job"`
	Role         string       `json:"role"`
	Kind         ProgressKind `json:"kind"`
	Text         string       `json:"text,omitempty"`
	ToolCall     string       `json:"tool_call,omitempty"`
	ToolStatus   string       `json:"tool_status,omitempty"`
	InputTokens  int          `json:"input_tokens,omitempty"`
	OutputTokens int          `json:"output_tokens,omitempty"`
	StopReason   string       `json:"stop_reason,omitempty"`
	Error        string       `json:"error,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	At           time.Time    `json:"at"`
}

// EventName implements Event.
func (AgentProgressed) EventName() string { return "AgentProgressed" }

// JobID implements Event.
func (e AgentProgressed) JobID() JobID { return e.Job }
