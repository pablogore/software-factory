// Package acpfake is an ACP agent that plays a script instead of thinking,
// so tests run the real ACP adapter against a real process without a model.
// It speaks the protocol on its own, apart from the adapter, and records what
// it received for the test to check.
//
// A test package runs it by re-running its own test binary: its TestMain
// calls Run when Requested, and Config launches that binary as the agent.
package acpfake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/platform/childenv"
)

const (
	envScript = "FACTORY_ACPFAKE_SCRIPT"
	envRecord = "FACTORY_ACPFAKE_RECORD"
	envChild  = "FACTORY_ACPFAKE_CHILD"
)

// Script is what the fake agent does.
type Script struct {
	// LoadSession announces session/load, so sessions can be resumed.
	LoadSession bool `json:"load_session,omitempty"`
	// NoHTTPMCP leaves out the HTTP MCP capability.
	NoHTTPMCP bool `json:"no_http_mcp,omitempty"`
	// Child makes the agent launch a child process that sleeps, as agents
	// that run tools do, when it creates its session.
	Child bool `json:"child,omitempty"`
	// IgnoreCancel makes the agent ignore session/cancel and keep running
	// when its stdin ends, so only killing it stops it.
	IgnoreCancel bool `json:"ignore_cancel,omitempty"`
	// Turns answers the prompts in order; a prompt past the last ends at
	// once with end_turn.
	Turns []Turn `json:"turns,omitempty"`
	// RecordEnv names the environment variables the agent records the
	// values of, as it was launched with them.
	RecordEnv []string `json:"record_env,omitempty"`
}

// Turn is how the fake answers one prompt.
type Turn struct {
	// Stderr is written on stderr, a line each, before the updates.
	Stderr  []string `json:"stderr,omitempty"`
	Updates []Update `json:"updates,omitempty"`
	// Usage, when set, is reported in the prompt's answer.
	Usage *Usage `json:"usage,omitempty"`
	// StopReason defaults to end_turn.
	StopReason string `json:"stop_reason,omitempty"`
	// Exit makes the agent exit after its updates, without answering.
	Exit bool `json:"exit,omitempty"`
	// Hang makes the agent work on after its updates until it is cancelled,
	// then answer with the cancelled stop reason.
	Hang bool `json:"hang,omitempty"`
}

// Update is one session/update: a message chunk, or a tool call when
// ToolCall is set. A tool call id seen before is sent as an update of it.
type Update struct {
	Message  string `json:"message,omitempty"`
	ToolCall string `json:"tool_call,omitempty"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Usage is the tokens a turn reports.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Header is an HTTP header of an MCP server, as ACP sends it.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MCPServer is an MCP server as the client sent it in session/new.
type MCPServer struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Headers []Header `json:"headers"`
}

// Received is what the fake agent got from the client.
type Received struct {
	// Args are the arguments the agent was launched with.
	Args []string `json:"args"`
	// Env holds the variables of Script.RecordEnv the agent was launched
	// with, as NAME=value.
	Env             []string    `json:"env"`
	ProtocolVersion int         `json:"protocol_version"`
	Cwd             string      `json:"cwd"`
	MCPServers      []MCPServer `json:"mcp_servers"`
	Session         string      `json:"session"`
	Prompts         []string    `json:"prompts"`
	Cancels         int         `json:"cancels"`
	// Loads holds the session id of each session/load.
	Loads []string `json:"loads"`
	// Children holds the pid of each child process the agent launched.
	Children []int `json:"children"`
}

// sessionID is the id the fake gives its only session.
const sessionID = "fake-session-1"

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// errExit is how a turn with Exit stops Serve.
var errExit = errors.New("scripted exit")

// Serve plays sc as an ACP agent, reading requests from in and writing to
// out until in ends, and the turns' Stderr to errOut, and calls record with what it received after every
// request or notification, before answering it.
func Serve(in io.Reader, out, errOut io.Writer, sc Script, record func(Received) error) error {
	f := &fake{sc: sc, out: json.NewEncoder(out), errOut: errOut, record: record, seen: map[string]bool{}}
	dec := json.NewDecoder(in)
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				if sc.IgnoreCancel {
					time.Sleep(time.Hour)
				}
				return nil
			}
			return fmt.Errorf("acpfake: read: %w", err)
		}
		if err := f.handle(m); err != nil {
			return err
		}
	}
}

type fake struct {
	sc     Script
	out    *json.Encoder
	errOut io.Writer
	record func(Received) error
	got    Received
	turns  int
	seen   map[string]bool
	// hanging is the prompt of a Hang turn, waiting for a cancel.
	hanging *message
}

func (f *fake) handle(m message) error {
	if len(m.ID) == 0 {
		if m.Method == "session/cancel" {
			f.got.Cancels++
			if err := f.record(f.got); err != nil {
				return err
			}
			if f.hanging != nil && !f.sc.IgnoreCancel {
				p := f.hanging
				f.hanging = nil
				return f.answer(*p, map[string]string{"stopReason": "cancelled"})
			}
		}
		return nil
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion int `json:"protocolVersion"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return f.fail(m, err)
		}
		f.got.ProtocolVersion = p.ProtocolVersion
		if err := f.record(f.got); err != nil {
			return err
		}
		return f.answer(m, map[string]any{
			"protocolVersion": 1,
			"agentCapabilities": map[string]any{
				"loadSession":     f.sc.LoadSession,
				"mcpCapabilities": map[string]bool{"http": !f.sc.NoHTTPMCP},
			},
			"authMethods": []any{},
		})
	case "session/new":
		var p struct {
			Cwd        string      `json:"cwd"`
			MCPServers []MCPServer `json:"mcpServers"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return f.fail(m, err)
		}
		f.got.Cwd, f.got.MCPServers, f.got.Session = p.Cwd, p.MCPServers, sessionID
		if f.sc.Child {
			pid, err := child()
			if err != nil {
				return f.fail(m, err)
			}
			f.got.Children = append(f.got.Children, pid)
		}
		if err := f.record(f.got); err != nil {
			return err
		}
		return f.answer(m, map[string]string{"sessionId": sessionID})
	case "session/load":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return f.fail(m, err)
		}
		f.got.Loads = append(f.got.Loads, p.SessionID)
		if err := f.record(f.got); err != nil {
			return err
		}
		return f.answer(m, map[string]any{})
	case "session/prompt":
		return f.prompt(m)
	default:
		return f.write(message{ID: m.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + m.Method}})
	}
}

func (f *fake) prompt(m message) error {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return f.fail(m, err)
	}
	text := ""
	for _, c := range p.Prompt {
		if c.Type == "text" {
			text += c.Text
		}
	}
	f.got.Prompts = append(f.got.Prompts, text)
	if err := f.record(f.got); err != nil {
		return err
	}
	var t Turn
	if f.turns < len(f.sc.Turns) {
		t = f.sc.Turns[f.turns]
	}
	f.turns++
	for _, line := range t.Stderr {
		if _, err := fmt.Fprintln(f.errOut, line); err != nil {
			return fmt.Errorf("acpfake: stderr: %w", err)
		}
	}
	for _, u := range t.Updates {
		if err := f.update(p.SessionID, u); err != nil {
			return err
		}
	}
	if t.Exit {
		return errExit
	}
	if t.Hang {
		f.hanging = &m
		return nil
	}
	res := map[string]any{"stopReason": "end_turn"}
	if t.StopReason != "" {
		res["stopReason"] = t.StopReason
	}
	if t.Usage != nil {
		res["usage"] = map[string]int{
			"inputTokens":  t.Usage.InputTokens,
			"outputTokens": t.Usage.OutputTokens,
			"totalTokens":  t.Usage.InputTokens + t.Usage.OutputTokens,
		}
	}
	return f.answer(m, res)
}

func (f *fake) update(session string, u Update) error {
	var up map[string]any
	switch {
	case u.ToolCall == "":
		up = map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": u.Message}}
	case f.seen[u.ToolCall]:
		up = map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": u.ToolCall, "status": u.Status, "content": []any{}}
		if u.Title != "" {
			up["title"] = u.Title
		}
	default:
		f.seen[u.ToolCall] = true
		up = map[string]any{"sessionUpdate": "tool_call", "toolCallId": u.ToolCall, "title": u.Title, "kind": "other", "status": u.Status, "content": []any{}}
	}
	params, err := json.Marshal(map[string]any{"sessionId": session, "update": up})
	if err != nil {
		return fmt.Errorf("acpfake: update: %w", err)
	}
	return f.write(message{Method: "session/update", Params: params})
}

func (f *fake) answer(m message, result any) error {
	return f.write(message{ID: m.ID, Result: result})
}

func (f *fake) fail(m message, err error) error {
	return f.write(message{ID: m.ID, Error: &rpcError{Code: -32602, Message: err.Error()}})
}

func (f *fake) write(m message) error {
	m.JSONRPC = "2.0"
	if err := f.out.Encode(m); err != nil {
		return fmt.Errorf("acpfake: write: %w", err)
	}
	return nil
}

// child launches this binary again as a child process that only sleeps,
// and leaves it running.
func child() (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("acpfake: own binary: %w", err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), envChild+"=1")
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("acpfake: child: %w", err)
	}
	return cmd.Process.Pid, nil
}

// Requested tells whether this process was launched to be the fake agent,
// or one of its children.
func Requested() bool { return os.Getenv(envScript) != "" }

// Run plays the script it was launched with on stdin and stdout and returns
// the exit code, recording to the file Config named. A child of the fake
// sleeps until it is killed.
func Run() int {
	if os.Getenv(envChild) != "" {
		time.Sleep(time.Hour)
		return 0
	}
	var sc Script
	if err := json.Unmarshal([]byte(os.Getenv(envScript)), &sc); err != nil {
		fmt.Fprintln(os.Stderr, "acpfake: script:", err)
		return 2
	}
	path := os.Getenv(envRecord)
	var env []string
	for _, name := range sc.RecordEnv {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	args := os.Args[1:]
	err := Serve(os.Stdin, os.Stdout, os.Stderr, sc, func(r Received) error {
		r.Args, r.Env = args, env
		return save(path, r)
	})
	switch {
	case errors.Is(err, errExit):
		return 3
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// Config launches this test binary as the fake agent playing sc, recording
// to the file record, with the jobs' HOMEs beside it; the test package's
// TestMain must call Run when Requested.
func Config(sc Script, record string) (acp.Config, error) {
	self, err := os.Executable()
	if err != nil {
		return acp.Config{}, fmt.Errorf("acpfake: own binary: %w", err)
	}
	b, err := json.Marshal(sc)
	if err != nil {
		return acp.Config{}, fmt.Errorf("acpfake: script: %w", err)
	}
	homes := childenv.Homes{Root: filepath.Join(filepath.Dir(record), "homes")}
	return acp.Config{Command: self, Env: []string{envScript + "=" + string(b), envRecord + "=" + record}, Homes: homes}, nil
}

// Load reads what the fake agent recorded in the file record; before it
// recorded anything, that is nothing.
func Load(record string) (Received, error) {
	var r Received
	b, err := os.ReadFile(record)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, fmt.Errorf("acpfake: record: %w", err)
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("acpfake: record: %w", err)
	}
	return r, nil
}

// save replaces the record whole, so Load never reads half of one.
func save(path string, r Received) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("acpfake: record: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("acpfake: record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("acpfake: record: %w", err)
	}
	return nil
}
