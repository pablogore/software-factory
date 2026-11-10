package mcpworker_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// packTool calls context_pack and returns its text and whether it is a
// tool error.
func packTool(ctx *specs.Context, cs *mcp.ClientSession) (string, bool) {
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: mcpworker.ToolContextPack})
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(len(res.Content)).ToEqual(1)
	text, ok := res.Content[0].(*mcp.TextContent)
	ctx.Expect(ok).ToEqual(true)
	return text.Text, res.IsError
}

func TestContextPackTool(t *testing.T) {
	specs.Describe(t, "the worker MCP server's context_pack", func(s *specs.Spec) {
		s.It("hands the bearer token and the job of its path to the submitter and answers the pack as text", func(ctx *specs.Context) {
			sub := &submitter{}
			cs := connect(ctx, mcpworker.Handler(sub), "j-1", "Bearer tok")
			text, isErr := packTool(ctx, cs)
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual("# Context pack of job \"j-1\", round 1\n")
			ctx.Expect(sub.packs).ToEqual([]call{{token: "tok", job: "j-1"}})
		})

		s.It("tells the agent to call it first", func(ctx *specs.Context) {
			cs := connect(ctx, mcpworker.Handler(&submitter{}), "j-1", "Bearer tok")
			res, err := cs.ListTools(context.Background(), nil)
			ctx.Expect(err).To(specs.BeNil())
			for _, tool := range res.Tools {
				if tool.Name == mcpworker.ToolContextPack {
					ctx.Expect(tool.Description).To(specs.StartWith("Call it first"))
					return
				}
			}
			ctx.Testing().Fatal("no context_pack tool")
		})

		s.It("answers a role that may not call it with a tool error worded as the HTTP route words it", func(ctx *specs.Context) {
			err := fmt.Errorf("worker: context pack: %w: role %q may not use tool %q", jobtoken.ErrForbidden, "intake", "context_pack")
			cs := connect(ctx, mcpworker.Handler(&submitter{err: err}), "j-1", "Bearer tok")
			text, isErr := packTool(ctx, cs)
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual("refused (403): " + err.Error())
		})
	})
}
