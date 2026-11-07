package ports

import (
	"context"
	"errors"

	"github.com/pablogore/software-factory/internal/domain"
)

// Agent errors. Callers compare with errors.Is.
var (
	// ErrResumeUnsupported means the agent cannot load an earlier session,
	// so the work goes on in a new session with a handover (01-diseno.md).
	ErrResumeUnsupported = errors.New("agent cannot resume a session")
	// ErrSessionClosed means the session was closed or its agent exited.
	ErrSessionClosed = errors.New("agent session closed")
	// ErrTurnRunning means a prompt was sent while the last one had not
	// ended: a session runs one turn at a time.
	ErrTurnRunning = errors.New("agent turn still running")
	// ErrAgentRefused means the agent answered a request with an error or
	// cannot do what the session asks, such as reach its MCP servers.
	ErrAgentRefused = errors.New("agent refused")
)

// MCPServer is an MCP server the agent reaches over HTTP, such as the one
// factoryd serves for the job with a token per session (01-diseno.md).
type MCPServer struct {
	Name    string
	URL     string
	Headers map[string]string
}

// SessionSpec is what a session is started with: the job and role it works
// for, the directory it works in and the MCP servers it may call.
type SessionSpec struct {
	Job        domain.JobID
	Role       string
	Dir        string
	MCPServers []MCPServer
}

// Agent starts sessions of one configured agent. Implementations hide which
// agent it is and how it is spoken to (ACP, a native CLI).
type Agent interface {
	// Start launches the agent and creates a session in s.Dir with
	// s.MCPServers. The session lives until Close, not until ctx ends.
	Start(ctx context.Context, s SessionSpec) (Session, error)
}

// Session is one agent session on one job.
type Session interface {
	// Prompt sends msg to the agent as it is. The channel carries what the
	// agent reports while it works, as progress events of the session's job
	// and role, and ends with one domain.ProgressEnded, then closes. Events
	// are dropped once ctx ends.
	Prompt(ctx context.Context, msg string) (<-chan domain.AgentProgressed, error)
	// Cancel asks the agent to stop the running turn; the turn still ends
	// with its ProgressEnded.
	Cancel() error
	// Resume loads the session again in the agent, or is
	// ErrResumeUnsupported.
	Resume(ctx context.Context) error
	// Close ends the session and its agent. Closing again is a no-op.
	Close() error
}
