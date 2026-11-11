package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/wiring"
)

// serve runs factoryd until ctx ends: it starts every piece, dispatches the
// bus and the outbox every tick, and on the way out waits for what runs up
// to the grace and closes the store.
func serve(ctx context.Context, args []string, out io.Writer) error {
	defaultState, err := wiring.DefaultDevServerState()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	config := fs.String("config", ".", "configuration root, with roles.yaml, roles/ and pipelines/")
	store := fs.String("store", "", "SQLite file jobs and their events are kept in")
	repo := fs.String("repo", ".", "git repo jobs' worktrees are made in")
	base := fs.String("base", "main", "branch jobs' worktrees start from")
	state := fs.String("dev-state", defaultState, "where running dev servers are recorded, so the next run kills what a crash left")
	concurrency := fs.Int("concurrency", 1, "agents that run at once")
	budget := fs.Float64("budget", 0, "USD a job may spend unless a person raised its own; must be above 0")
	costs := fs.String("costs", "", "where jobs' spend comes from: "+wiring.CostsSubscription+" counts 0 USD; with none, no agent is launched")
	listen := fs.String("listen", "127.0.0.1:7317", "address the worker API and the jobs' MCP servers listen on for the agents")
	timeout := fs.Duration("session-timeout", 2*time.Hour, "how long a role's session may run before it is cut and its attempt fails")
	tick := fs.Duration("tick", time.Second, "how often due retries and the outbox are dispatched")
	grace := fs.Duration("shutdown-grace", 30*time.Second, "how long a shutdown waits for what runs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkGitVersion(ctx); err != nil {
		return err
	}
	homes, err := childenv.DefaultHomes()
	if err != nil {
		return err
	}
	clock := wiring.SystemClock{}
	srv, err := wiring.StartServer(ctx, wiring.ServeConfig{
		Config: *config, Store: *store, Repo: *repo, Base: *base, DevState: *state, Homes: homes,
		Concurrency: *concurrency, Budget: *budget, Costs: *costs, Listen: *listen, Agents: wiring.ACPAgents(homes, clock),
		SessionTimeout: *timeout, Tick: *tick, Grace: *grace, Clock: clock, Log: out,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "factoryd serving %s with the configuration in %s, dispatching every %s; worker API on %s\n", *store, *config, *tick, srv.Addr())
	if err := srv.Run(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "factoryd stopped; store closed")
	return nil
}
