package wiring_test

import (
	"context"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/domain"
)

// listed names the tools cs's session is shown.
func listed(ctx *specs.Context, cs *mcp.ClientSession) []string {
	res, err := cs.ListTools(context.Background(), nil)
	ctx.Expect(err).To(specs.BeNil())
	names := []string{}
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestDemoReviewerTools is the S4 demo: through the worker API, with the
// roles the repo ships, a reviewer's session is not shown run_gate and is
// refused it all the same when it calls it, running no check, while the
// builder's is shown it and runs the checks as before.
func TestDemoReviewerTools(t *testing.T) {
	specs.Describe(t, "the tools a session of the worker API is shown", func(s *specs.Spec) {
		s.It("leaves run_gate out of a reviewer's tools", func(ctx *specs.Context) {
			w := newWorkerWorld(ctx)
			cs := w.session(ctx, repoRoot, "reviewer-code")
			ctx.Expect(listed(ctx, cs)).ToEqual([]string{"ask_human", "context_pack", "dev_server", "progress", "submit_result"})
		})

		s.It("lists run_gate to the builder, whose call runs the checks as before", func(ctx *specs.Context) {
			w := newWorkerWorld(ctx)
			cs := w.session(ctx, repoRoot, "builder")
			ctx.Expect(listed(ctx, cs)).To(specs.Contain(mcpworker.ToolRunGate))

			text, isErr := call(ctx, cs, mcpworker.ToolRunGate, `{}`)

			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).To(specs.StartWith("checks pass for job j-1: `make check` exited 0"))
			ctx.Expect(w.gate.checked()).ToEqual([]domain.JobID{"j-1"})
		})

		s.It("refuses a reviewer that calls run_gate anyway, running no check", func(ctx *specs.Context) {
			w := newWorkerWorld(ctx)
			cs := w.session(ctx, repoRoot, "reviewer-code")

			text, isErr := call(ctx, cs, mcpworker.ToolRunGate, `{}`)

			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual(`refused (403): worker: run gate: job token not allowed: role "reviewer-code" may not use tool "run_gate"`)
			ctx.Expect(w.gate.checked()).To(specs.BeEmpty())
			ctx.Expect(w.feed(ctx)).To(specs.BeEmpty())
		})
	})
}
