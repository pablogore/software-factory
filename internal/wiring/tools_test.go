package wiring_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/wiring"
)

// repoCopy is a configuration root with the repo's roles.yaml, roles/ and
// pipelines/, in the test's own directory so a role file can be changed.
func repoCopy(ctx *specs.Context) string {
	dir := ctx.Testing().TempDir()
	for _, sub := range []string{"roles", "pipelines"} {
		ctx.Expect(os.CopyFS(filepath.Join(dir, sub), os.DirFS(filepath.Join(repoRoot, sub)))).To(specs.BeNil())
	}
	agents, err := os.ReadFile(filepath.Join(repoRoot, "roles.yaml"))
	ctx.Expect(err).To(specs.BeNil())
	write(ctx, dir, "roles.yaml", string(agents))
	return dir
}

// setBuilderTools gives dir's builder, as the repo ships it, the tools line
// tools.
func setBuilderTools(ctx *specs.Context, dir, tools string) {
	write(ctx, dir, "roles/builder.yaml", "name: builder\nkind: build\nprompt: roles/builder.md\nagent: claude\nmodel: opus\nmode: blocking\n"+tools+"\n")
}

// TestDemoRoleTools is the S1 demo: a misspelt tool stops the roles from
// loading, the repo's roles all load, and a session launched as for a real
// job is refused a tool its role from the YAML does not list.
func TestDemoRoleTools(t *testing.T) {
	specs.Describe(t, "a role's tools in factoryd serve", func(s *specs.Spec) {
		s.It("loads every role the repo ships, with only the worker's tools", func(ctx *specs.Context) {
			files, err := filepath.Glob(filepath.Join(repoRoot, "roles", "*.yaml"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(files).To(specs.HaveLen(len(repoRoles)))
			for _, r := range repoRoles {
				rs, err := wiring.LoadRoleSession(repoRoot, r.name)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(rs.Role.Validate()).To(specs.BeNil())
			}
			_, err = wiring.ValidateRoles(repoRoot)
			ctx.Expect(err).To(specs.BeNil())
		})

		s.It("fails config validate naming the role and a misspelt tool", func(ctx *specs.Context) {
			dir := repoCopy(ctx)
			setBuilderTools(ctx, dir, "tools: [run_gat]")

			_, err := wiring.ValidateRoles(dir)

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).To(specs.Contain(`role builder: invalid value: unknown tool "run_gat"`))
		})

		s.It("fails to start the server naming the role and a misspelt tool", func(ctx *specs.Context) {
			clk := newTickClock()
			cfg := launchConfig(ctx, clk, &fakeAgents{dir: ctx.Testing().TempDir()}, &syncBuffer{})
			cfg.Config = repoCopy(ctx)
			setBuilderTools(ctx, cfg.Config, "tools: [submit_result, run_gat]")

			srv, err := wiring.StartServer(context.Background(), cfg)

			ctx.Expect(srv == nil).To(specs.BeTrue())
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(err.Error()).To(specs.Contain(`role builder: invalid value: unknown tool "run_gat"`))
		})

		s.It("refuses a launched builder a tool its YAML does not list, naming the role and the tool", func(ctx *specs.Context) {
			clk := newTickClock()
			hang := acpfake.Script{Turns: []acpfake.Turn{{Hang: true}}}
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{hang}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			// run_gate and dev_server are a builder's to use, but this one
			// does not list them.
			cfg.Config = repoCopy(ctx)
			setBuilderTools(ctx, cfg.Config, "tools: [submit_result, progress]")
			seedBuild(ctx, cfg.Store, "j-1")
			srv, err := wiring.StartServer(context.Background(), cfg)
			ctx.Expect(err).To(specs.BeNil())
			run, stop := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Run(run) }()
			deadline := time.Now().Add(20 * time.Second)
			for len(agents.received(ctx, 0).Prompts) == 0 {
				if time.Now().After(deadline) {
					ctx.Testing().Fatal("the session never got its prompt")
				}
				time.Sleep(20 * time.Millisecond)
			}
			url, token := bearerOf(ctx, agents.received(ctx, 0))
			cs := connect(ctx, url, token)

			text, isErr := call(ctx, cs, "run_gate", `{}`)
			ctx.Expect(isErr).To(specs.BeTrue())
			ctx.Expect(text).To(specs.Contain(`role "builder" may not use tool "run_gate"`))
			text, isErr = call(ctx, cs, "dev_server", `{"action":"start"}`)
			ctx.Expect(isErr).To(specs.BeTrue())
			ctx.Expect(text).To(specs.Contain(`role "builder" may not use tool "dev_server"`))
			// The tools it lists still work for it.
			_, isErr = call(ctx, cs, "progress", `{"message":"building"}`)
			ctx.Expect(isErr).To(specs.BeFalse())
			ctx.Expect(agents.launched()).ToEqual([]string{"builder"})

			stop()
			ctx.Expect(serverWaitAsked(ctx, clk)).ToEqual(serveTick)
			ctx.Expect(serverWaitAsked(ctx, clk)).ToEqual(serveGrace)
			clk.Advance(serveGrace)
			ctx.Expect(<-done).To(specs.MatchError(wiring.ErrShutdownGrace))
		})
	})
}
