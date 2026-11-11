package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/pablogore/software-factory/internal/ports"
)

// JSON-RPC's error codes for a method the client does not serve, for params
// it cannot read and for a result it cannot write.
const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// message is one JSON-RPC 2.0 message: a request (ID and Method), a
// notification (Method only) or a response (ID with Result or Error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// refusal is the agent's error answer to a request, kept whole so a limit
// can be read from its code, message and data.
type refusal struct{ rpcError }

func (r *refusal) Error() string {
	return fmt.Sprintf("acp: %s (%d): %v", r.Message, r.Code, ports.ErrAgentRefused)
}

func (r *refusal) Unwrap() error { return ports.ErrAgentRefused }

// reply handles the response to one request. It runs on the reading
// goroutine, so it sees every notification sent before the response.
type reply func(result json.RawMessage, err error)

// conn speaks JSON-RPC over the agent's stdin and stdout, one JSON value per
// line as ACP does.
type conn struct {
	wmu sync.Mutex
	w   io.Writer

	mu      sync.Mutex
	next    int64
	pending map[int64]reply
	err     error // why the connection closed; nil while open

	notified func(method string, params json.RawMessage)
	// requested answers a request of the agent with its result, or with
	// the error the agent gets instead.
	requested func(method string, params json.RawMessage) (any, *rpcError)
	done      chan struct{}
}

func newConn(w io.Writer, notified func(method string, params json.RawMessage), requested func(method string, params json.RawMessage) (any, *rpcError)) *conn {
	return &conn{w: w, pending: map[int64]reply{}, notified: notified, requested: requested, done: make(chan struct{})}
}

// send writes a request and hands its response to r. When send returns an
// error, r is never called.
func (c *conn) send(method string, params any, r reply) error {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.next++
	id := c.next
	c.pending[id] = r
	c.mu.Unlock()

	raw, _ := json.Marshal(id)
	if err := c.write(message{ID: raw, Method: method}, params); err != nil {
		c.mu.Lock()
		_, still := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if still {
			return err
		}
		// The connection closed meanwhile and already answered r.
	}
	return nil
}

// call sends a request and decodes its result into result, which may be nil.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	type answer struct {
		result json.RawMessage
		err    error
	}
	got := make(chan answer, 1)
	if err := c.send(method, params, func(res json.RawMessage, err error) { got <- answer{res, err} }); err != nil {
		return err
	}
	select {
	case a := <-got:
		if a.err != nil {
			return a.err
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(a.result, result); err != nil {
			return fmt.Errorf("acp: %s result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("acp: %s: %w", method, ctx.Err())
	}
}

func (c *conn) notify(method string, params any) error {
	c.mu.Lock()
	err := c.err
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return c.write(message{Method: method}, params)
}

func (c *conn) write(m message, params any) error {
	m.JSONRPC = "2.0"
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("acp: encode %s: %w", m.Method, err)
		}
		m.Params = raw
	}
	line, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("acp: encode %s: %w", m.Method, err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("acp: write %s: %w: %w", m.Method, ports.ErrSessionClosed, err)
	}
	return nil
}

// read handles what the agent writes until it stops, then answers every
// request still waiting with ErrSessionClosed.
func (c *conn) read(r io.Reader) {
	defer close(c.done)
	dec := json.NewDecoder(r)
	var why error
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			why = err
			break
		}
		c.handle(m)
	}
	if errors.Is(why, io.EOF) {
		why = errors.New("agent exited")
	}
	c.mu.Lock()
	c.err = fmt.Errorf("acp: %w: %w", ports.ErrSessionClosed, why)
	pending := c.pending
	c.pending = map[int64]reply{}
	c.mu.Unlock()
	for _, r := range pending {
		r(nil, c.err)
	}
}

func (c *conn) handle(m message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		c.answer(m)
	case m.Method != "":
		c.notified(m.Method, m.Params)
	default:
		var id int64
		if err := json.Unmarshal(m.ID, &id); err != nil {
			return
		}
		c.mu.Lock()
		r, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if !ok {
			return
		}
		if m.Error != nil {
			r(nil, &refusal{*m.Error})
			return
		}
		r(m.Result, nil)
	}
}

// answer writes the response to the agent's request m. A method the client
// does not serve is an error the agent hears, so it does not wait.
func (c *conn) answer(m message) {
	result, rerr := c.requested(m.Method, m.Params)
	if rerr != nil {
		_ = c.write(message{ID: m.ID, Error: rerr}, nil)
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		_ = c.write(message{ID: m.ID, Error: &rpcError{Code: codeInternalError, Message: err.Error()}}, nil)
		return
	}
	_ = c.write(message{ID: m.ID, Result: raw}, nil)
}
