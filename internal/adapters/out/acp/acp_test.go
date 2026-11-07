package acp_test

import (
	"context"
	"os"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

// TestMain lets this test binary be the fake agent the tests launch.
func TestMain(m *testing.M) {
	if acpfake.Requested() {
		os.Exit(acpfake.Run())
	}
	os.Exit(m.Run())
}

// fakeAgent is the ACP adapter launching the scripted fake agent.
func fakeAgent(ctx *specs.Context, sc acpfake.Script) (ports.Agent, func() acpfake.Received) {
	record := recordIn(ctx)
	cfg, err := acpfake.Config(sc, record)
	ctx.Expect(err).To(specs.BeNil())
	return acp.New(cfg, stopped{}), func() acpfake.Received {
		got, err := acpfake.Load(record)
		ctx.Expect(err).To(specs.BeNil())
		return got
	}
}

func TestAgentContract(t *testing.T) {
	specs.Describe(t, "acp.Agent with the scripted fake ACP agent", func(s *specs.Spec) {
		agentContract(s, fakeAgent)
	})
}

func TestAgentLaunch(t *testing.T) {
	specs.Describe(t, "acp.Agent", func(s *specs.Spec) {
		s.It("fails to start a session when its command cannot be launched", func(ctx *specs.Context) {
			a := acp.New(acp.Config{Command: "/nonexistent/acp-agent", Args: []string{"--acp"}}, stopped{})

			_, err := a.Start(context.Background(), ports.SessionSpec{Job: jobID, Role: role, Dir: ctx.Testing().TempDir()})

			ctx.Expect(err).To(specs.MatchError(os.ErrNotExist))
		})

		s.It("refuses to start a session without a command", func(ctx *specs.Context) {
			a := acp.New(acp.Config{}, stopped{})

			_, err := a.Start(context.Background(), ports.SessionSpec{Job: jobID, Role: role, Dir: ctx.Testing().TempDir()})

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
