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

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
)

const (
	envScript = "FACTORY_ACPFAKE_SCRIPT"
	envRecord = "FACTORY_ACPFAKE_RECORD"
)

// Script is what the fake agent does.
type Script struct {
	// LoadSession announces session/load, so sessions can be resumed.
	LoadSession bool `json:"load_session,omitempty"`
	// NoHTTPMCP leaves out the HTTP MCP capability.
	NoHTTPMCP bool `json:"no_http_mcp,omitempty"`
	// Turns answers the prompts in order; a prompt past the last ends at
	// once with end_turn.
	Turns []Turn `json:"turns,omitempty"`
}

// Turn is how the fake answers one prompt.
type Turn struct {
	Updates []Update `json:"updates,omitempty"`
	// Usage, when set, is reported in the prompt's answer.
	Usage *Usage `json:"usage,omitempty"`
	// StopReason defaults to end_turn.
	StopReason string `json:"stop_reason,omitempty"`
	// Exit makes the agent exit after its updates, without answering.
	Exit bool `json:"exit,omitempty"`
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
	ProtocolVersion int         `json:"protocol_version"`
	Cwd             string      `json:"cwd"`
	MCPServers      []MCPServer `json:"mcp_servers"`
	Session         string      `json:"session"`
	Prompts         []string    `json:"prompts"`
	Cancels         int         `json:"cancels"`
	// Loads holds the session id of each session/load.
	Loads []string `json:"loads"`
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
// out until in ends, and calls record with what it received after every
// request or notification, before answering it.
func Serve(in io.Reader, out io.Writer, sc Script, record func(Received) error) error {
	f := &fake{sc: sc, out: json.NewEncoder(out), record: record, seen: map[string]bool{}}
	dec := json.NewDecoder(in)
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
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
	record func(Received) error
	got    Received
	turns  int
	seen   map[string]bool
}

func (f *fake) handle(m message) error {
	if len(m.ID) == 0 {
		if m.Method == "session/cancel" {
			f.got.Cancels++
			return f.record(f.got)
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
	for _, u := range t.Updates {
		if err := f.update(p.SessionID, u); err != nil {
			return err
		}
	}
	if t.Exit {
		return errExit
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

// Requested tells whether this process was launched to be the fake agent.
func Requested() bool { return os.Getenv(envScript) != "" }

// Run plays the script it was launched with on stdin and stdout and returns
// the exit code, recording to the file Config named.
func Run() int {
	var sc Script
	if err := json.Unmarshal([]byte(os.Getenv(envScript)), &sc); err != nil {
		fmt.Fprintln(os.Stderr, "acpfake: script:", err)
		return 2
	}
	path := os.Getenv(envRecord)
	err := Serve(os.Stdin, os.Stdout, sc, func(r Received) error { return save(path, r) })
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
// to the file record; the test package's TestMain must call Run when
// Requested.
func Config(sc Script, record string) (acp.Config, error) {
	self, err := os.Executable()
	if err != nil {
		return acp.Config{}, fmt.Errorf("acpfake: own binary: %w", err)
	}
	b, err := json.Marshal(sc)
	if err != nil {
		return acp.Config{}, fmt.Errorf("acpfake: script: %w", err)
	}
	return acp.Config{Command: self, Env: []string{envScript + "=" + string(b), envRecord + "=" + record}}, nil
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
