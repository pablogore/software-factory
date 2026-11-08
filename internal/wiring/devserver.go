package wiring

import (
	"context"
	"fmt"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/portrange"
	"github.com/pablogore/software-factory/internal/ports"
)

// DevServerFile is where a repo configures its dev server, from the repo
// root.
const DevServerFile = ".factory/devserver.yaml"

// DevServers is what the worker's dev_server runs a session's dev server
// with: Servers runs it in the job's worktree from Worktrees, on a port of
// the job's range in Ports, with the command DevServerFile sets at the base
// commit Bases recorded for the job, so a job's branch never picks what its
// dev_server runs.
type DevServers struct {
	Servers   ports.DevServers
	Worktrees ports.Workspace
	Ports     *portrange.Allocator
	Bases     ports.BaseLog
}

// baseDevCommand reads DevServerFile as the job's base commit has it.
type baseDevCommand struct{ bases ports.BaseLog }

func (b baseDevCommand) DevCommand(ctx context.Context, job domain.JobID, wt ports.Worktree) ([]string, error) {
	base, err := b.bases.Base(ctx, job)
	if err != nil {
		return nil, err
	}
	data, ok, err := gitdiff.Differ{}.AtBaseIfAny(ctx, wt.Path, base, DevServerFile)
	if err != nil || !ok {
		return nil, err
	}
	d, err := configyaml.LoadDevServer([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", DevServerFile, base, err)
	}
	return d.Command, nil
}
