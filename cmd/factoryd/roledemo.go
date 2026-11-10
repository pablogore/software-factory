package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/wiring"
)

// roleDemoPromptLines is how much of the prompt the demo prints.
const roleDemoPromptLines = 6

// roleDemo launches a session of a role read from a configuration root, as
// factoryd does, but with the fake ACP agent in place of the agent's own
// command: the fake is launched with the agent's args and env, model filled
// in, and gets the role's prompt. It prints the agent, the model and what the
// fake received, so a person can change the role's YAML and run it again.
func roleDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("role-demo", flag.ContinueOnError)
	dir := fs.String("config", ".", "configuration root, with roles.yaml and roles/")
	role := fs.String("role", "builder", "role to launch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rs, err := wiring.LoadRoleSession(*dir, *role)
	if err != nil {
		return err
	}
	var names []string
	for _, kv := range rs.Agent.Env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	work, err := os.MkdirTemp("", "factoryd-role-demo-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	record := filepath.Join(work, "received.json")
	cfg, err := acpfake.Config(acpfake.Script{RecordEnv: names}, record)
	if err != nil {
		return err
	}
	cfg.Args, cfg.PassEnv, cfg.UserHome = rs.Agent.Args, rs.Agent.PassEnv, rs.Agent.UserHome
	cfg.Env = append(cfg.Env, rs.Agent.Env...)

	_, _ = fmt.Fprintf(out, "role %s (%s) from %s\n", rs.Role.Name, rs.Role.Kind, *dir)
	_, _ = fmt.Fprintf(out, "agent %s: %s, launched here as the fake ACP agent\n", rs.Role.Agent, commandLine(rs.Agent))
	_, _ = fmt.Fprintf(out, "model %s\n", rs.Role.Model)

	sess, err := acp.New(cfg, wiring.SystemClock{}).Start(ctx, ports.SessionSpec{Job: "role-demo", Role: rs.Role.Name, Kind: rs.Role.Kind, Dir: work})
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	ch, err := sess.Prompt(ctx, rs.Prompt)
	if err != nil {
		return err
	}
	for ev := range ch {
		if ev.Kind == domain.ProgressEnded && ev.Error != "" {
			return fmt.Errorf("role demo: turn ended with %s", ev.Error)
		}
	}
	got, err := acpfake.Load(record)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "the agent was launched with args [%s] and env [%s]\n", strings.Join(got.Args, " "), strings.Join(got.Env, " "))
	if len(got.Prompts) != 1 {
		return fmt.Errorf("role demo: the agent got %d prompts, want 1", len(got.Prompts))
	}
	lines := strings.Split(strings.TrimRight(got.Prompts[0], "\n"), "\n")
	_, _ = fmt.Fprintf(out, "the agent got the prompt of %s (%d lines), starting:\n", rs.Role.Prompt, len(lines))
	_, _ = fmt.Fprintln(out, indent(strings.Join(lines[:min(len(lines), roleDemoPromptLines)], "\n")))
	return nil
}

// commandLine is how the agent's own command would be run.
func commandLine(a acp.Config) string {
	return strings.Join(append(append(append([]string{}, a.Env...), a.Command), a.Args...), " ")
}
