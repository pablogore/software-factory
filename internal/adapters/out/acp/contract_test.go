package acp_test

import (
	"context"
	"path/filepath"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

const (
	jobID domain.JobID = "job-1"
	role  string       = "builder"
)

var epoch = time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)

// stopped is a clock that never moves, so every event carries epoch.
type stopped struct{}

func (stopped) Now() time.Time { return epoch }

// newAgentFunc returns an agent that plays sc, and what it received so far.
type newAgentFunc func(ctx *specs.Context, sc acpfake.Script) (ports.Agent, func() acpfake.Received)

// progress is an event of the contract's job and role at epoch.
func progress(ev domain.AgentProgressed) domain.AgentProgressed {
	ev.Job, ev.Role, ev.At = jobID, role, epoch
	return ev
}

// drain reads a turn's events until the channel closes, failing instead of
// hanging when the agent never ends the turn.
func drain(ctx *specs.Context, ch <-chan domain.AgentProgressed) []domain.AgentProgressed {
	var got []domain.AgentProgressed
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-time.After(10 * time.Second):
			ctx.Errorf("turn did not end; got %v", got)
			return got
		}
	}
}

// agentContract is what every ports.Agent does with an agent that plays a
// script: the session it creates, the prompt it sends and the progress it
// publishes.
func agentContract(s *specs.Spec, newAgent newAgentFunc) {
	bg := context.Background()
	mcp := []ports.MCPServer{{
		Name:    "factoryd",
		URL:     "http://127.0.0.1:7777/mcp",
		Headers: map[string]string{"X-Job": "job-1", "Authorization": "Bearer session-token"},
	}}
	spec := func(ctx *specs.Context) ports.SessionSpec {
		return ports.SessionSpec{Job: jobID, Role: role, Dir: ctx.Testing().TempDir(), MCPServers: mcp}
	}
	start := func(ctx *specs.Context, a ports.Agent, sp ports.SessionSpec) ports.Session {
		sess, err := a.Start(bg, sp)
		ctx.Expect(err).To(specs.BeNil())
		ctx.Cleanup(func() { _ = sess.Close() })
		return sess
	}

	s.When("a session starts", func(s *specs.Spec) {
		s.It("the agent creates it in the job's directory with the job's MCP servers", func(ctx *specs.Context) {
			a, received := newAgent(ctx, acpfake.Script{})
			sp := spec(ctx)

			start(ctx, a, sp)

			got := received()
			ctx.Expect(got.ProtocolVersion).ToEqual(1)
			ctx.Expect(got.Cwd).ToEqual(sp.Dir)
			ctx.Expect(got.MCPServers).ToEqual([]acpfake.MCPServer{{
				Type:    "http",
				Name:    "factoryd",
				URL:     "http://127.0.0.1:7777/mcp",
				Headers: []acpfake.Header{{Name: "Authorization", Value: "Bearer session-token"}, {Name: "X-Job", Value: "job-1"}},
			}})
		})

		s.It("the agent creates it with no MCP servers when the job has none", func(ctx *specs.Context) {
			a, received := newAgent(ctx, acpfake.Script{NoHTTPMCP: true})
			sp := spec(ctx)
			sp.MCPServers = nil

			start(ctx, a, sp)

			ctx.Expect(received().MCPServers).To(specs.HaveLen(0))
		})

		s.It("is refused when the job has MCP servers the agent cannot reach over HTTP", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{NoHTTPMCP: true})

			_, err := a.Start(bg, spec(ctx))

			ctx.Expect(err).To(specs.MatchError(ports.ErrAgentRefused))
		})

		s.It("is refused without a directory", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{})
			sp := spec(ctx)
			sp.Dir = ""

			_, err := a.Start(bg, sp)

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})

	s.When("the session is prompted", func(s *specs.Spec) {
		s.It("the agent receives the role prompt as factoryd passed it", func(ctx *specs.Context) {
			a, received := newAgent(ctx, acpfake.Script{})
			sess := start(ctx, a, spec(ctx))
			prompt := "You are the builder.\n\nBuild S1: «lanzar agentes».\n  - keep the tree clean\n"

			ch, err := sess.Prompt(bg, prompt)
			ctx.Expect(err).To(specs.BeNil())
			drain(ctx, ch)

			ctx.Expect(received().Prompts).ToEqual([]string{prompt})
		})

		s.It("publishes the agent's messages, tool calls and token usage as progress events of the job", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{Turns: []acpfake.Turn{{
				Updates: []acpfake.Update{
					{Message: "Reading the spec."},
					{ToolCall: "call-1", Title: "Read spec.md", Status: "pending"},
					{ToolCall: "call-1", Status: "completed"},
					{Message: "Done."},
				},
				Usage: &acpfake.Usage{InputTokens: 1200, OutputTokens: 340},
			}}})
			sess := start(ctx, a, spec(ctx))

			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(drain(ctx, ch)).ToEqual([]domain.AgentProgressed{
				progress(domain.AgentProgressed{Kind: domain.ProgressMessage, Text: "Reading the spec."}),
				progress(domain.AgentProgressed{Kind: domain.ProgressToolCall, ToolCall: "call-1", Text: "Read spec.md", ToolStatus: "pending"}),
				progress(domain.AgentProgressed{Kind: domain.ProgressToolCall, ToolCall: "call-1", ToolStatus: "completed"}),
				progress(domain.AgentProgressed{Kind: domain.ProgressMessage, Text: "Done."}),
				progress(domain.AgentProgressed{Kind: domain.ProgressUsage, InputTokens: 1200, OutputTokens: 340}),
				progress(domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: "end_turn"}),
			})
		})

		s.It("ends the turn with the agent's stop reason and no usage when it reports none", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{Turns: []acpfake.Turn{{StopReason: "max_tokens"}}})
			sess := start(ctx, a, spec(ctx))

			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(drain(ctx, ch)).ToEqual([]domain.AgentProgressed{
				progress(domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: "max_tokens"}),
			})
		})

		s.It("takes another prompt once the turn ended", func(ctx *specs.Context) {
			a, received := newAgent(ctx, acpfake.Script{})
			sess := start(ctx, a, spec(ctx))

			for _, p := range []string{"first", "second"} {
				ch, err := sess.Prompt(bg, p)
				ctx.Expect(err).To(specs.BeNil())
				drain(ctx, ch)
			}

			ctx.Expect(received().Prompts).ToEqual([]string{"first", "second"})
		})

		s.It("ends the turn with an error when the agent exits in the middle of it", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{Turns: []acpfake.Turn{{Updates: []acpfake.Update{{Message: "bye"}}, Exit: true}}})
			sess := start(ctx, a, spec(ctx))

			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())
			got := drain(ctx, ch)

			ctx.Expect(got).To(specs.HaveLen(2))
			ctx.Expect(got[0]).ToEqual(progress(domain.AgentProgressed{Kind: domain.ProgressMessage, Text: "bye"}))
			ctx.Expect(got[1].Kind).ToEqual(domain.ProgressEnded)
			ctx.Expect(got[1].Error).To(specs.Contain(ports.ErrSessionClosed.Error()))
			_, err = sess.Prompt(bg, "again")
			ctx.Expect(err).To(specs.MatchError(ports.ErrSessionClosed))
		})
	})

	s.When("the session is cancelled", func(s *specs.Spec) {
		s.It("the agent is asked to cancel its session, and the session ends", func(ctx *specs.Context) {
			a, received := newAgent(ctx, acpfake.Script{})
			sess := start(ctx, a, spec(ctx))

			ctx.Expect(sess.Cancel()).To(specs.BeNil())

			ctx.Expect(received().Cancels).ToEqual(1)
			_, err := sess.Prompt(bg, "after")
			ctx.Expect(err).To(specs.MatchError(ports.ErrSessionClosed))
		})

		s.It("the running turn ends as cancelled", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{Turns: []acpfake.Turn{{Updates: []acpfake.Update{{Message: "Working."}}, Hang: true}}})
			sess := start(ctx, a, spec(ctx))
			ch, err := sess.Prompt(bg, "build")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(sess.Cancel()).To(specs.BeNil())

			ctx.Expect(drain(ctx, ch)).ToEqual([]domain.AgentProgressed{
				progress(domain.AgentProgressed{Kind: domain.ProgressMessage, Text: "Working."}),
				progress(domain.AgentProgressed{Kind: domain.ProgressEnded, StopReason: domain.StopCancelled}),
			})
		})

		s.It("cancelling again is a no-op", func(ctx *specs.Context) {
			a, received := newAgent(ctx, acpfake.Script{})
			sess := start(ctx, a, spec(ctx))

			ctx.Expect(sess.Cancel()).To(specs.BeNil())
			ctx.Expect(sess.Cancel()).To(specs.BeNil())

			ctx.Expect(received().Cancels).ToEqual(1)
		})
	})

	s.When("the session is resumed", func(s *specs.Spec) {
		s.It("the agent loads the same session when it can", func(ctx *specs.Context) {
			a, received := newAgent(ctx, acpfake.Script{LoadSession: true})
			sess := start(ctx, a, spec(ctx))

			ctx.Expect(sess.Resume(bg)).To(specs.BeNil())

			got := received()
			ctx.Expect(got.Loads).ToEqual([]string{got.Session})
		})

		s.It("is unsupported when the agent cannot load sessions", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{})
			sess := start(ctx, a, spec(ctx))

			ctx.Expect(sess.Resume(bg)).To(specs.MatchError(ports.ErrResumeUnsupported))
		})
	})

	s.When("the session is closed", func(s *specs.Spec) {
		s.It("takes no more prompts, and closing again is a no-op", func(ctx *specs.Context) {
			a, _ := newAgent(ctx, acpfake.Script{})
			sess := start(ctx, a, spec(ctx))

			ctx.Expect(sess.Close()).To(specs.BeNil())
			ctx.Expect(sess.Close()).To(specs.BeNil())

			_, err := sess.Prompt(bg, "late")
			ctx.Expect(err).To(specs.MatchError(ports.ErrSessionClosed))
		})
	})
}

// recordIn is a fresh record file for one fake agent.
func recordIn(ctx *specs.Context) string {
	return filepath.Join(ctx.Testing().TempDir(), "received.json")
}
