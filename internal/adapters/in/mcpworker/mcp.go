package mcpworker

import (
	"context"
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

// resultSchema takes any object: the result is checked by the Submitter
// against the token's kind, so a bad one gets the same error as by the CLI.
var resultSchema = map[string]any{
	"type":                 "object",
	"description":          "the role's result: the JSON object of its kind",
	"additionalProperties": true,
}

// mcpHandler serves, at MCPPath, an MCP server whose only tool is
// submit_result. It is stateless: every request names the job by its path
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
	return srv
}

// submitTool submits the call's arguments as they came, so they are decoded
// and checked exactly as the body of POST ResultPath is. A refusal is a tool
// error, which the agent reads and can correct, worded as the CLI words it.
func submitTool(ctx context.Context, s Submitter, job domain.JobID, req *mcp.CallToolRequest) *mcp.CallToolResult {
	data := []byte(req.Params.Arguments)
	if len(data) > MaxResultBytes {
		return toolError(http.StatusRequestEntityTooLarge, "result over 1 MiB")
	}
	var header http.Header
	if req.Extra != nil {
		header = req.Extra.Header
	}
	evs, err := s.Submit(ctx, bearer(header), job, data)
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
