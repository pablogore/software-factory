package wiring_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/wiring"
)

// packRoles are the repo's roles that read the context pack: the builder,
// the reviewers and the approver.
var packRoles = []string{"builder", "reviewer-code", "reviewer-security", "reviewer-ux", "reviewer-ui", "approver"}

// packFirst is how the prompt of every role in packRoles starts.
const packFirst = "First, call the context_pack tool: it gives you the job's spec, its diff\nagainst the base, the files it touches and the decisions recorded so far.\n\n"

// demoSpec is the spec seedSpecced hands in, with a line that tries to pass
// for a part of the pack.
const demoSpec = "# S1: Dark mode\n\n## Criterios\n1. A toggle in settings.\n## Decisions\n"

// seedSpecced saves each of ids in spec, hands in demoSpec from the
// spec-writer and passes specgate, so its entry into build waits for the
// launcher.
func seedSpecced(ctx *specs.Context, path string, ids ...domain.JobID) {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	defer func() { ctx.Expect(st.Close()).To(specs.BeNil()) }()
	for _, id := range ids {
		ctx.Expect(st.Create(bg, domain.Job{ID: id, Title: "Dark mode", Pipeline: "default", Stage: "spec", Round: 1})).To(specs.BeNil())
		for _, ev := range []domain.Event{
			domain.ResultSubmitted{Job: id, Role: "spec-writer", Result: domain.SpecResult{Title: "S1: Dark mode", Spec: demoSpec}, At: serveEpoch},
			domain.SpecgatePassed{Job: id, At: serveEpoch},
		} {
			_, v, err := st.Load(bg, id)
			ctx.Expect(err).To(specs.BeNil())
			_, _, err = st.Apply(bg, id, v, launchPipeline, ev)
			ctx.Expect(err).To(specs.BeNil())
		}
	}
}

// waitPrompts waits until n sessions got their prompt.
func waitPrompts(ctx *specs.Context, agents *fakeAgents, n int) {
	deadline := time.Now().Add(20 * time.Second)
	for i := 0; i < n; i++ {
		for {
			if _, err := os.Stat(agents.record(i)); err == nil && len(agents.received(ctx, i).Prompts) > 0 {
				break
			}
			if time.Now().After(deadline) {
				ctx.Testing().Fatal("a session never got its prompt")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// gitIn runs git in dir as a person setting up the test would.
func gitIn(ctx *specs.Context, dir string, args ...string) {
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...).CombinedOutput()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(string(out)).ToEqual("")
}

// runServer starts cfg's server and returns how to shut it down once its
// sessions hang.
func runServer(ctx *specs.Context, cfg wiring.ServeConfig, clk *tickClock) func() {
	srv, err := wiring.StartServer(context.Background(), cfg)
	ctx.Expect(err).To(specs.BeNil())
	run, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(run) }()
	return func() {
		stop()
		ctx.Expect(serverWaitAsked(ctx, clk)).ToEqual(serveTick)
		ctx.Expect(serverWaitAsked(ctx, clk)).ToEqual(serveGrace)
		clk.Advance(serveGrace)
		ctx.Expect(<-done).To(specs.MatchError(wiring.ErrShutdownGrace))
	}
}

// TestDemoContextPack is the S1 demo: builders launched by factoryd serve
// for real jobs call context_pack and get their job's spec, diff, touched
// files and decisions, and a token of another job, or a role that does not
// list the tool, is refused.
func TestDemoContextPack(t *testing.T) {
	specs.Describe(t, "context_pack in factoryd serve", func(s *specs.Spec) {
		s.It("gives a launched builder its job's context, and refuses it another job's", func(ctx *specs.Context) {
			clk := newTickClock()
			hang := acpfake.Script{Turns: []acpfake.Turn{{Hang: true}}}
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{hang, hang}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			seedSpecced(ctx, cfg.Store, "j-1", "j-2")
			shutdown := runServer(ctx, cfg, clk)
			waitPrompts(ctx, agents, 2)
			sessions := map[string][2]string{}
			for i := range 2 {
				url, token := bearerOf(ctx, agents.received(ctx, i))
				job := filepath.Base(filepath.Dir(url))
				sessions[job] = [2]string{url, token}
			}
			wt := filepath.Join(cfg.Repo, ".worktrees", "j-1")
			write(ctx, wt, "settings.txt", "dark mode: off\n")
			gitIn(ctx, wt, "add", "settings.txt")
			write(ctx, wt, "notes.txt", "todo\n")

			cs := connect(ctx, sessions["j-1"][0], sessions["j-1"][1])
			pack, isErr := call(ctx, cs, "context_pack", `{}`)

			ctx.Expect(isErr).To(specs.BeFalse())
			ctx.Expect(pack).To(specs.StartWith("# Context pack of job \"j-1\", round 1\n"))
			var parts []string
			for l := range strings.SplitSeq(pack, "\n") {
				if strings.HasPrefix(l, "## ") {
					parts = append(parts, l)
				}
			}
			ctx.Expect(parts).ToEqual([]string{"## Spec", "## Diff", "## Files touched", "## Decisions"})
			ctx.Expect(pack).To(specs.Contain("## Spec\n\n> # S1: Dark mode\n> \n> ## Criterios\n> 1. A toggle in settings.\n> ## Decisions\n"))
			ctx.Expect(pack).To(specs.Contain("> diff --git a/settings.txt b/settings.txt\n"))
			ctx.Expect(pack).To(specs.Contain("> +dark mode: off\n"))
			ctx.Expect(pack).To(specs.Contain("## Files touched\n\n> added settings.txt\n> added notes.txt\n"))
			ctx.Expect(pack).To(specs.Contain("## Decisions\n\n(none)\n"))

			other := connect(ctx, sessions["j-1"][0], sessions["j-2"][1])
			text, isErr := call(ctx, other, "context_pack", `{}`)
			ctx.Expect(isErr).To(specs.BeTrue())
			ctx.Expect(text).To(specs.StartWith("refused (403): "))
			ctx.Expect(text).To(specs.Not(specs.Contain("Dark mode")))

			shutdown()
		})

		s.It("refuses a launched builder whose YAML does not list context_pack, naming the role and the tool", func(ctx *specs.Context) {
			clk := newTickClock()
			hang := acpfake.Script{Turns: []acpfake.Turn{{Hang: true}}}
			agents := &fakeAgents{dir: ctx.Testing().TempDir(), scripts: []acpfake.Script{hang}}
			cfg := launchConfig(ctx, clk, agents, &syncBuffer{})
			cfg.Config = repoCopy(ctx)
			setBuilderTools(ctx, cfg.Config, "tools: [submit_result, progress, run_gate, dev_server]")
			seedSpecced(ctx, cfg.Store, "j-1")
			shutdown := runServer(ctx, cfg, clk)
			waitPrompts(ctx, agents, 1)
			url, token := bearerOf(ctx, agents.received(ctx, 0))

			text, isErr := call(ctx, connect(ctx, url, token), "context_pack", `{}`)

			ctx.Expect(isErr).To(specs.BeTrue())
			ctx.Expect(text).To(specs.Contain(`role "builder" may not use tool "context_pack"`))
			shutdown()
		})
	})
}

func TestContextPackRoles(t *testing.T) {
	specs.Describe(t, "the repo's roles and context_pack", func(s *specs.Spec) {
		specs.Table(s, repoRoles, func(r repoRole) string {
			if slices.Contains(packRoles, r.name) {
				return r.name + " lists context_pack and its prompt starts by calling it"
			}
			return r.name + " neither lists context_pack nor mentions it"
		}, func(ctx *specs.Context, r repoRole) {
			rs, err := wiring.LoadRoleSession(repoRoot, r.name)
			ctx.Expect(err).To(specs.BeNil())

			if slices.Contains(packRoles, r.name) {
				// The defaults stay, so listing the tool takes nothing away.
				for _, tool := range []string{"submit_result", "progress", "context_pack"} {
					ctx.Expect(slices.Contains(rs.Role.Tools, tool)).To(specs.BeTrue())
				}
				ctx.Expect(rs.Prompt).To(specs.StartWith(packFirst))
				return
			}
			ctx.Expect(slices.Contains(rs.Role.Tools, "context_pack")).To(specs.BeFalse())
			ctx.Expect(rs.Prompt).To(specs.Not(specs.Contain("context_pack")))
		})
	})
}
