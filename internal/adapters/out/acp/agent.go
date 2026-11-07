// Package acp runs any agent that speaks the Agent Client Protocol
// (JSON-RPC over stdio), launched by its configured command, such as
// `gemini --acp` or Claude Code's ACP adapter (01-diseno.md).
package acp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// protocolVersion is the ACP version the client speaks.
const protocolVersion = 1

// DefaultGrace is how long a session waits for its agent to stop when asked
// before it kills it, when Config.Grace is not set.
const DefaultGrace = 5 * time.Second

// Config is how an agent is launched: its command, its arguments and the
// environment it gets on top of factoryd's, and how long its sessions last.
type Config struct {
	Command string
	Args    []string
	Env     []string
	// Timeout is how long a session may run from its start before it is
	// ended as a cancelled one is; zero means it never times out.
	Timeout time.Duration
	// Grace is how long the agent has to stop when a session is cancelled
	// or times out; zero means DefaultGrace.
	Grace time.Duration
}

// Agent launches one process of the configured agent per session.
type Agent struct {
	cfg   Config
	clock ports.Clock
}

var _ ports.Agent = (*Agent)(nil)

// New returns an agent launched with cfg. clock dates the progress events.
func New(cfg Config, clock ports.Clock) *Agent {
	return &Agent{cfg: cfg, clock: clock}
}

// Start launches the agent in s.Dir and creates an ACP session there with
// s.MCPServers. A failure after launch stops the agent again.
func (a *Agent) Start(ctx context.Context, s ports.SessionSpec) (ports.Session, error) {
	if a.cfg.Command == "" {
		return nil, fmt.Errorf("acp: agent without a command: %w", domain.ErrMissingField)
	}
	if s.Dir == "" {
		return nil, fmt.Errorf("acp: session without a directory: %w", domain.ErrMissingField)
	}
	dir, err := filepath.Abs(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("acp: directory %q: %w", s.Dir, err)
	}
	cmd := exec.Command(a.cfg.Command, a.cfg.Args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), a.cfg.Env...)
	ownGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin: %w", err)
	}
	// An own pipe, not StdoutPipe: Wait does not close it, so Close decides
	// when reading stops.
	out, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout: %w", err)
	}
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		_ = out.Close()
		_ = w.Close()
		return nil, fmt.Errorf("acp: launch %s: %w", a.cfg.Command, err)
	}
	_ = w.Close()

	sess := &session{agent: a, spec: s, dir: dir, cmd: cmd, stdin: stdin, stdout: out, closed: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(sess.exited)
	}()
	sess.conn = newConn(stdin, sess.notified)
	go sess.conn.read(out)
	if err := sess.open(ctx); err != nil {
		_ = sess.Close()
		return nil, err
	}
	if a.cfg.Timeout > 0 {
		sess.mu.Lock()
		sess.timer = time.AfterFunc(a.cfg.Timeout, func() { sess.stop(domain.StopTimedOut) })
		sess.mu.Unlock()
	}
	return sess, nil
}

// grace is how long the agent has to stop when asked.
func (a *Agent) grace() time.Duration {
	if a.cfg.Grace > 0 {
		return a.cfg.Grace
	}
	return DefaultGrace
}

type initializeResult struct {
	ProtocolVersion   int `json:"protocolVersion"`
	AgentCapabilities struct {
		LoadSession     bool `json:"loadSession"`
		MCPCapabilities struct {
			HTTP bool `json:"http"`
		} `json:"mcpCapabilities"`
	} `json:"agentCapabilities"`
}

// open runs ACP's handshake and creates the session.
func (s *session) open(ctx context.Context) error {
	var init initializeResult
	err := s.conn.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	}, &init)
	if err != nil {
		return fmt.Errorf("acp: initialize: %w", err)
	}
	if init.ProtocolVersion != protocolVersion {
		return fmt.Errorf("acp: agent speaks protocol %d, not %d: %w", init.ProtocolVersion, protocolVersion, ports.ErrAgentRefused)
	}
	if len(s.spec.MCPServers) > 0 && !init.AgentCapabilities.MCPCapabilities.HTTP {
		return fmt.Errorf("acp: agent cannot reach MCP servers over HTTP: %w", ports.ErrAgentRefused)
	}
	s.loadable = init.AgentCapabilities.LoadSession

	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := s.conn.call(ctx, "session/new", s.where(), &created); err != nil {
		return fmt.Errorf("acp: session/new: %w", err)
	}
	if created.SessionID == "" {
		return fmt.Errorf("acp: session/new without a session id: %w", ports.ErrAgentRefused)
	}
	s.id = created.SessionID
	return nil
}

type header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type mcpServer struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Headers []header `json:"headers"`
}

// where is the directory and MCP servers that session/new and session/load
// send. ACP asks for both arrays even when empty.
func (s *session) where() map[string]any {
	servers := make([]mcpServer, 0, len(s.spec.MCPServers))
	for _, m := range s.spec.MCPServers {
		hs := make([]header, 0, len(m.Headers))
		for name, value := range m.Headers {
			hs = append(hs, header{Name: name, Value: value})
		}
		slices.SortFunc(hs, func(a, b header) int { return strings.Compare(a.Name, b.Name) })
		servers = append(servers, mcpServer{Type: "http", Name: m.Name, URL: m.URL, Headers: hs})
	}
	return map[string]any{"cwd": s.dir, "mcpServers": servers}
}
