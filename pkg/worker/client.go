// Package worker is the client of factoryd's worker API, for agents without
// MCP: `factory worker submit-result` hands a role's result over with it.
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
)

// TokenEnv is the environment variable a session's agent finds its job
// token in.
const TokenEnv = "FACTORY_JOB_TOKEN"

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

// SubmitResult posts result, a JSON value, as job's result. A refusal is a
// *RefusedError.
func (c Client) SubmitResult(ctx context.Context, job string, result []byte) (Accepted, error) {
	u := strings.TrimSuffix(c.URL, "/") + "/worker/jobs/" + url.PathEscape(job) + "/result"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(result))
	if err != nil {
		return Accepted{}, fmt.Errorf("submit result: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Accepted{}, fmt.Errorf("submit result: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Accepted{}, fmt.Errorf("submit result: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var r struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &r) != nil || r.Error == "" {
			r.Error = strings.TrimSpace(string(body))
		}
		return Accepted{}, &RefusedError{Status: resp.StatusCode, Message: r.Error}
	}
	var a Accepted
	if err := json.Unmarshal(body, &a); err != nil {
		return Accepted{}, fmt.Errorf("submit result: answer: %w", err)
	}
	return a, nil
}
