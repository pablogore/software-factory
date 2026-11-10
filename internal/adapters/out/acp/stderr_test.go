package acp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/platform/stderrtail"
	"github.com/pablogore/software-factory/internal/ports"
)

// stderrOf runs the fake agent playing sc, prompts it once per turn and
// closes the session, so all it wrote on stderr was read, and returns the
// session's Stderr.
func stderrOf(ctx *specs.Context, sc acpfake.Script) []string {
	a, _ := fakeAgent(ctx, sc)
	sess, err := a.Start(context.Background(), ports.SessionSpec{Job: jobID, Role: role, Dir: ctx.Testing().TempDir()})
	ctx.Expect(err).To(specs.BeNil())
	for range sc.Turns {
		ch, err := sess.Prompt(context.Background(), "work")
		ctx.Expect(err).To(specs.BeNil())
		drain(ctx, ch)
	}
	ctx.Expect(sess.Close()).To(specs.BeNil())
	return sess.Stderr()
}

func TestAgentStderr(t *testing.T) {
	specs.Describe(t, "acp.Agent's stderr", func(s *specs.Spec) {
		s.It("keeps what the agent wrote on stderr in the session, over its turns", func(ctx *specs.Context) {
			got := stderrOf(ctx, acpfake.Script{Turns: []acpfake.Turn{
				{Stderr: []string{"warning: slow network"}},
				{Stderr: []string{"error: model overloaded", "giving up"}},
			}})
			ctx.Expect(got).ToEqual([]string{"warning: slow network", "error: model overloaded", "giving up"})
		})

		s.It("has none when the agent wrote nothing on stderr", func(ctx *specs.Context) {
			ctx.Expect(stderrOf(ctx, acpfake.Script{Turns: []acpfake.Turn{{}}})).To(specs.BeNil())
		})

		s.It("keeps only the last lines, a line too long cut", func(ctx *specs.Context) {
			lines := make([]string, 0, 2*stderrtail.MaxLines)
			for i := range 2 * stderrtail.MaxLines {
				lines = append(lines, fmt.Sprintf("line %d", i))
			}
			lines = append(lines, strings.Repeat("x", 10*stderrtail.MaxLineBytes))
			got := stderrOf(ctx, acpfake.Script{Turns: []acpfake.Turn{{Stderr: lines}}})
			ctx.Expect(got).To(specs.HaveLen(stderrtail.MaxLines))
			ctx.Expect(got[0]).ToEqual(fmt.Sprintf("line %d", stderrtail.MaxLines+1))
			ctx.Expect(got[stderrtail.MaxLines-1]).ToEqual(strings.Repeat("x", stderrtail.MaxLineBytes) + stderrtail.Cut)
		})
	})
}
