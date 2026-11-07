package mcpworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pablogore/software-factory/internal/domain"
)

// MCPPath is the streamable HTTP endpoint of a job's MCP server; {job} is
// its ID. The session's agent reaches it with its job token as a bearer
// token.
const MCPPath = "/worker/jobs/{job}/mcp"

// ToolSubmitResult is the MCP tool a role's result is handed over with.
const ToolSubmitResult = "submit_result"

// ToolProgress is the MCP tool an agent of any role reports what it is doing
// with, while it works.
const ToolProgress = "progress"

// resultSchema takes any object: the result is checked by the Submitter
// against the token's kind, so a bad one gets the same error as by the CLI.
var resultSchema = map[string]any{
	"type":                 "object",
	"description":          "the role's result: the JSON object of its kind",
	"additionalProperties": true,
}

// progressSchema is the progress tool's one argument. The Submitter checks
// the message, so an empty or too long one is refused as a tool error.
var progressSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"message": map[string]any{
			"type":        "string",
			"description": fmt.Sprintf("what you are doing now, in at most %d characters", domain.MaxProgressText),
		},
	},
	"required":             []any{"message"},
	"additionalProperties": false,
}

// mcpHandler serves, at MCPPath, an MCP server whose only tools are
// submit_result and progress. It is stateless: every request names the job by its path
// and brings its token, so no session outlives a request or carries a job
// or a token from one request to the next.
func mcpHandler(s Submitter) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer(s, domain.JobID(r.PathValue("job")))
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 2 * MaxResultBytes})
}

func mcpServer(s Submitter, job domain.JobID) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "factoryd-worker", Version: "1"}, nil)
	srv.AddTool(&mcp.Tool{
		Name: ToolSubmitResult,
		Description: "Hands the role's result to factoryd as the job's result, as `factory worker submit-result` does. " +
			"The arguments are the result itself, the JSON object of the role's kind. " +
			"A refused result is not recorded; fix what the error names and call again.",
		InputSchema: resultSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return submitTool(ctx, s, job, req), nil
	})
	srv.AddTool(&mcp.Tool{
		Name: ToolProgress,
		Description: "Tells whoever follows the job what you are doing, while you work: the message is recorded as an event of the job. " +
			"Call it as often as helps; it does not hand in your result, submit_result does.",
		InputSchema: progressSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return progressTool(ctx, s, job, req), nil
	})
	return srv
}

// progressTool records the call's message on job. Arguments that are not
// one message are refused before the Submitter sees them.
func progressTool(ctx context.Context, s Submitter, job domain.JobID, req *mcp.CallToolRequest) *mcp.CallToolResult {
	var args struct {
		Message *string `json:"message"`
	}
	dec := json.NewDecoder(bytes.NewReader(req.Params.Arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil || args.Message == nil {
		return toolError(http.StatusUnprocessableEntity, `arguments must be {"message": "<text>"}`)
	}
	evs, err := s.Progress(ctx, bearer(header(req)), job, *args.Message)
	if err != nil {
		return toolError(refusal(err))
	}
	a := accepted(job, evs)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("progress recorded for job %s: %s", a.Job, strings.Join(a.Events, ", "))}},
		StructuredContent: a,
	}
}

// header is the HTTP header req came with, or nil.
func header(req *mcp.CallToolRequest) http.Header {
	if req.Extra == nil {
		return nil
	}
	return req.Extra.Header
}

// submitTool submits the call's arguments as they came, so they are decoded
// and checked exactly as the body of POST ResultPath is. A refusal is a tool
// error, which the agent reads and can correct, worded as the CLI words it.
func submitTool(ctx context.Context, s Submitter, job domain.JobID, req *mcp.CallToolRequest) *mcp.CallToolResult {
	data := []byte(req.Params.Arguments)
	if len(data) > MaxResultBytes {
		return toolError(http.StatusRequestEntityTooLarge, "result over 1 MiB")
	}
	evs, err := s.Submit(ctx, bearer(header(req)), job, data)
	if err != nil {
		return toolError(refusal(err))
	}
	a := accepted(job, evs)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("result recorded for job %s: %s", a.Job, strings.Join(a.Events, ", "))}},
		StructuredContent: a,
	}
}

func toolError(code int, msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("refused (%d): %s", code, msg)}},
	}
}
