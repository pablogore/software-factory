package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/stderrtail"
	"github.com/pablogore/software-factory/internal/ports"
)

// session is one ACP session in its own agent process.
type session struct {
	agent    *Agent
	spec     ports.SessionSpec
	dir      string
	cmd      *exec.Cmd
	stdin    io.Closer
	stdout   *os.File
	conn     *conn
	id       string
	loadable bool
	// stderr keeps the last lines the agent writes on stderr; os/exec
	// copies them in on its own goroutine until the agent exits.
	stderr *stderrtail.Tail
	// exited ends once the agent process has exited and been reaped.
	exited chan struct{}

	mu   sync.Mutex
	turn *turn
	// reason is why factoryd stops the session, once it does: the stop
	// reason a running turn ends with, whatever the agent answers.
	reason    string
	timer     *time.Timer
	stopOnce  sync.Once
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
	// done ends once out is closed.
	done chan struct{}
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
	t := &turn{ctx: ctx, out: make(chan domain.AgentProgressed, 16), done: make(chan struct{})}
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
// A turn of a session factoryd stopped ends with factoryd's reason, also
// when the agent had to be killed and never answered. An error answer that
// is the agent's usage or rate limit ends it with domain.StopLimited.
func (s *session) end(t *turn, res json.RawMessage, err error) {
	var r promptResult
	if err == nil {
		if derr := json.Unmarshal(res, &r); derr != nil {
			err = fmt.Errorf("acp: session/prompt result: %w", derr)
		}
	}
	s.mu.Lock()
	reason := s.reason
	s.mu.Unlock()
	switch {
	case reason != "":
		if err == nil && r.Usage != nil {
			s.emit(t, domain.AgentProgressed{Kind: domain.ProgressUsage, InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens})
		}
		s.emit(t, domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: reason})
	case err != nil:
		if why, until, ok := limited(err, s.agent.clock.Now()); ok {
			s.emit(t, domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: domain.StopLimited, Reason: why, ResumeAt: until})
			break
		}
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
	close(t.done)
}

// emit dates ev and names the job and role, then hands it to the turn. A
// consumer that does not read holds the agent's output back; one whose ctx
// ended, or whose session is closing, gets nothing more than the channel
// still has room for, so a closing session's last events are not lost.
func (s *session) emit(t *turn, ev domain.AgentProgressed) {
	ev.Job, ev.Role, ev.At = s.spec.Job, s.spec.Role, s.agent.clock.Now()
	select {
	case t.out <- ev:
		return
	default:
	}
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

// Cancel stops the session as cancelled.
func (s *session) Cancel() error {
	s.stop(domain.StopCancelled)
	return nil
}

// stop ends the session for reason, first asking the agent: ACP's
// session/cancel for the running turn, then closing its stdin so it exits.
// Whatever is left after the grace period is killed by Close. Stopping again
// waits for the first stop and changes nothing.
func (s *session) stop(reason string) {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		if s.reason == "" {
			s.reason = reason
		}
		t := s.turn
		s.mu.Unlock()

		expired := make(chan struct{})
		grace := time.AfterFunc(s.agent.grace(), func() { close(expired) })
		defer grace.Stop()
		// A connection already closed has no agent left to ask.
		_ = s.conn.notify("session/cancel", map[string]string{"sessionId": s.id})
		if t != nil {
			select {
			case <-t.done:
			case <-expired:
			}
		}
		_ = s.stdin.Close()
		select {
		case <-s.exited:
		case <-expired:
		}
		_ = s.Close()
	})
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

// Stderr is the last lines the agent has written on stderr, as far as they
// were read: all of them once the session is closed.
func (s *session) Stderr() []string {
	return s.stderr.Lines()
}

// Close kills the agent's process group and waits for the agent, then stops
// reading its output, so a running turn ends with ErrSessionClosed, or with
// the reason the session was stopped for.
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		if s.timer != nil {
			s.timer.Stop()
		}
		s.mu.Unlock()
		close(s.closed)
		killGroup(s.cmd)
		<-s.exited
		_ = s.stdout.Close()
		<-s.conn.done
	})
	return nil
}
