package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// session is one ACP session in its own agent process.
type session struct {
	agent    *Agent
	spec     ports.SessionSpec
	dir      string
	cmd      *exec.Cmd
	stdout   *os.File
	conn     *conn
	id       string
	loadable bool

	mu        sync.Mutex
	turn      *turn
	closeOnce sync.Once
	// closed ends when Close starts, so no event waits on a reader then.
	closed chan struct{}
}

var _ ports.Session = (*session)(nil)

// turn is one prompt and the events the agent reports until it answers.
// Only the reading goroutine sends on out and closes it.
type turn struct {
	ctx context.Context
	out chan domain.AgentProgressed
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Prompt sends msg as the turn's only text block.
func (s *session) Prompt(ctx context.Context, msg string) (<-chan domain.AgentProgressed, error) {
	s.mu.Lock()
	if s.turn != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("acp: prompt: %w", ports.ErrTurnRunning)
	}
	t := &turn{ctx: ctx, out: make(chan domain.AgentProgressed, 16)}
	s.turn = t
	s.mu.Unlock()

	params := map[string]any{"sessionId": s.id, "prompt": []textContent{{Type: "text", Text: msg}}}
	err := s.conn.send("session/prompt", params, func(res json.RawMessage, err error) { s.end(t, res, err) })
	if err != nil {
		s.mu.Lock()
		s.turn = nil
		s.mu.Unlock()
		return nil, fmt.Errorf("acp: prompt: %w", err)
	}
	return t.out, nil
}

type promptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

// end reports the agent's answer to the turn's prompt and closes the turn.
func (s *session) end(t *turn, res json.RawMessage, err error) {
	var r promptResult
	if err == nil {
		if derr := json.Unmarshal(res, &r); derr != nil {
			err = fmt.Errorf("acp: session/prompt result: %w", derr)
		}
	}
	switch {
	case err != nil:
		s.emit(t, domain.AgentProgressed{Kind: domain.ProgressEnded, Error: err.Error()})
	default:
		if r.Usage != nil {
			s.emit(t, domain.AgentProgressed{Kind: domain.ProgressUsage, InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens})
		}
		s.emit(t, domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: r.StopReason})
	}
	s.mu.Lock()
	s.turn = nil
	s.mu.Unlock()
	close(t.out)
}

// emit dates ev and names the job and role, then hands it to the turn. A
// consumer that does not read holds the agent's output back; one whose ctx
// ended, or whose session is closing, gets nothing more.
func (s *session) emit(t *turn, ev domain.AgentProgressed) {
	ev.Job, ev.Role, ev.At = s.spec.Job, s.spec.Role, s.agent.clock.Now()
	select {
	case t.out <- ev:
	case <-t.ctx.Done():
	case <-s.closed:
	}
}

type sessionUpdate struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		SessionUpdate string `json:"sessionUpdate"`
		// Content is one block in a message chunk and a list in a tool
		// call, so it is decoded only where it is a block.
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		Status     string          `json:"status"`
	} `json:"update"`
}

// notified turns the agent's session/update notifications into progress of
// the running turn. Updates outside a turn (a session/load replaying its
// history) and kinds with no progress event (thoughts, plans) are dropped.
func (s *session) notified(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var u sessionUpdate
	if err := json.Unmarshal(params, &u); err != nil || u.SessionID != s.id {
		return
	}
	s.mu.Lock()
	t := s.turn
	s.mu.Unlock()
	if t == nil {
		return
	}
	switch u.Update.SessionUpdate {
	case "agent_message_chunk":
		var c textContent
		if err := json.Unmarshal(u.Update.Content, &c); err != nil || c.Type != "text" {
			return
		}
		s.emit(t, domain.AgentProgressed{Kind: domain.ProgressMessage, Text: c.Text})
	case "tool_call", "tool_call_update":
		s.emit(t, domain.AgentProgressed{Kind: domain.ProgressToolCall, ToolCall: u.Update.ToolCallID, Text: u.Update.Title, ToolStatus: u.Update.Status})
	}
}

// Cancel sends ACP's session/cancel; the agent then answers the prompt with
// the cancelled stop reason.
func (s *session) Cancel() error {
	if err := s.conn.notify("session/cancel", map[string]string{"sessionId": s.id}); err != nil {
		return fmt.Errorf("acp: cancel: %w", err)
	}
	return nil
}

// Resume loads the session again with ACP's session/load, which only agents
// that announce loadSession serve.
func (s *session) Resume(ctx context.Context) error {
	if !s.loadable {
		return fmt.Errorf("acp: resume: %w", ports.ErrResumeUnsupported)
	}
	s.mu.Lock()
	running := s.turn != nil
	s.mu.Unlock()
	if running {
		return fmt.Errorf("acp: resume: %w", ports.ErrTurnRunning)
	}
	params := s.where()
	params["sessionId"] = s.id
	if err := s.conn.call(ctx, "session/load", params, nil); err != nil {
		return fmt.Errorf("acp: session/load: %w", err)
	}
	return nil
}

// Close kills the agent and waits for it, then stops reading its output, so
// a running turn ends with ErrSessionClosed.
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
		_ = s.stdout.Close()
		<-s.conn.done
	})
	return nil
}
