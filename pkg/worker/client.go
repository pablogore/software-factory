// Package worker is the client of factoryd's worker API, for agents without
// MCP: `factory worker submit-result` hands a role's result over with it and
// `factory worker run-gate` runs the job's checks.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenEnv is the environment variable a session's agent finds its job
// token in.
const TokenEnv = "FACTORY_JOB_TOKEN"

// maxAnswer is the most of an answer the client reads: a run of the checks
// carries their whole output, twice, in its run and its report.
const maxAnswer = 64 << 20

// Accepted is factoryd's answer to a result it decided: the job and the
// names of the events the job saved.
type Accepted struct {
	Job    string   `json:"job"`
	Events []string `json:"events"`
}

// RefusedError is factoryd's answer to a result it refused: Status is the
// HTTP status and Message what was wrong.
type RefusedError struct {
	Status  int
	Message string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("refused (%d): %s", e.Status, e.Message)
}

// Client calls the worker API at URL with Token.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// GateRun is factoryd's answer to a run of a job's checks: whether they
// pass, as the gate would judge them, the run, and Report, the run worded as
// the MCP tool run_gate words it.
type GateRun struct {
	Job    string    `json:"job"`
	Passed bool      `json:"passed"`
	Run    ChecksRun `json:"run"`
	Report string    `json:"report"`
}

// ChecksRun is one run of a job's checks: the command, its exit code, how
// long it took, its output, the rules the diff broke and the ones that did
// not run, and why.
type ChecksRun struct {
	Command    string        `json:"command"`
	ExitCode   int           `json:"exit_code"`
	Duration   time.Duration `json:"duration"`
	Output     string        `json:"output"`
	Violations []string      `json:"violations,omitempty"`
	Notes      []string      `json:"notes,omitempty"`
}

// SubmitResult posts result, a JSON value, as job's result. A refusal is a
// *RefusedError.
func (c Client) SubmitResult(ctx context.Context, job string, result []byte) (Accepted, error) {
	var a Accepted
	if err := c.post(ctx, "submit result", job, "result", bytes.NewReader(result), &a); err != nil {
		return Accepted{}, err
	}
	return a, nil
}

// RunGate runs job's checks, as the gate does, and returns the run. It
// sends nothing but the token and the job: the checks are the job's. A
// refusal is a *RefusedError.
func (c Client) RunGate(ctx context.Context, job string) (GateRun, error) {
	var g GateRun
	if err := c.post(ctx, "run gate", job, "gate", http.NoBody, &g); err != nil {
		return GateRun{}, err
	}
	return g, nil
}

// post posts body to job's route and decodes factoryd's answer into out;
// op names the call in its errors.
func (c Client) post(ctx context.Context, op, job, route string, body io.Reader, out any) error {
	u := strings.TrimSuffix(c.URL, "/") + "/worker/jobs/" + url.PathEscape(job) + "/" + route
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if resp.StatusCode != http.StatusOK {
		var r struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &r) != nil || r.Error == "" {
			r.Error = strings.TrimSpace(string(data))
		}
		return &RefusedError{Status: resp.StatusCode, Message: r.Error}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: answer: %w", op, err)
	}
	return nil
}
