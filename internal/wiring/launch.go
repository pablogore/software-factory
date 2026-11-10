package wiring

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pablogore/software-factory/internal/adapters/in/mcpworker"
	"github.com/pablogore/software-factory/internal/adapters/out/acp"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
)

// LaunchSubscriber is the name the bus keeps role session launches under.
const LaunchSubscriber = "worker.launch"

// CostsSubscription is the cost source of agents paid by a subscription,
// not by use: a job's spend counts 0 USD, so the budget never stops one.
const CostsSubscription = "subscription"

// ErrUnknownCostSource means serve was given a cost source it does not
// know.
var ErrUnknownCostSource = errors.New("unknown cost source")

// Agents gives the agent a role's sessions run on, from the role's
// configuration.
type Agents func(rs RoleSession) ports.Agent

// ACPAgents runs every role's agent over ACP as roles.yaml configures it,
// with its HOME under homes unless it uses the operator's. clock dates what
// the agent reports.
func ACPAgents(homes childenv.Homes, clock ports.Clock) Agents {
	return func(rs RoleSession) ports.Agent {
		cfg := rs.Agent
		cfg.Homes = homes
		return acp.New(cfg, clock)
	}
}

// roleAgents reads roles from a configuration root as their sessions start
// and starts them on the agent agents gives.
type roleAgents struct {
	dir    string
	agents Agents
}

func (r roleAgents) Role(_ context.Context, name string) (domain.Role, string, error) {
	rs, err := LoadRoleSession(r.dir, name)
	return rs.Role, rs.Prompt, err
}

// Start reads role again, so the session runs on the agent the configuration
// names now, as its prompt was read.
func (r roleAgents) Start(ctx context.Context, role domain.Role, s ports.SessionSpec) (ports.Session, error) {
	rs, err := LoadRoleSession(r.dir, role.Name)
	if err != nil {
		return nil, err
	}
	return r.agents(rs).Start(ctx, s)
}

// mcpURL is the URL of job's worker MCP server under base, the worker
// API's.
func mcpURL(base string, job domain.JobID) string {
	return base + strings.Replace(mcpworker.MCPPath, "{job}", string(job), 1)
}

// costSource is the scheduler's cost source serve was configured with: none
// fails every spend, so no agent launches, and CostsSubscription spends 0
// USD.
func costSource(name string, jobs ports.JobStore) (scheduler.CostSource, error) {
	switch name {
	case "":
		return unpricedCosts{jobs}, nil
	case CostsSubscription:
		return subscriptionCosts{unpricedCosts{jobs}}, nil
	}
	return nil, fmt.Errorf("%w: %q, want %q or none", ErrUnknownCostSource, name, CostsSubscription)
}

// subscriptionCosts spends nothing, since a subscription is not paid by use;
// a job's own budget is still read from the store.
type subscriptionCosts struct{ unpricedCosts }

func (subscriptionCosts) Spent(context.Context, domain.JobID) (float64, error) { return 0, nil }
