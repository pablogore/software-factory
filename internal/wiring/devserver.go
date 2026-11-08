package wiring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/adapters/out/devserver"
	"github.com/pablogore/software-factory/internal/adapters/out/gitdiff"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/platform/portrange"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/worker"
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

	// sub is the Submitter Worker runs them from.
	sub *worker.Submitter
}

// DefaultDevServerState is where factoryd records its running dev servers,
// under the user's cache directory, so the next run kills what a crash
// left.
func DefaultDevServerState() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "factoryd", "devservers"), nil
}

// StartDevServers is how factoryd starts its dev servers, with records in
// state: first it kills every dev server an earlier run left there,
// checking each recorded pid is still that run's process.
func StartDevServers(ctx context.Context, state string) (*devserver.Servers, error) {
	return devserver.Registered(ctx, devserver.DefaultGrace, state)
}

// baseDevCommand reads DevServerFile as the job's base commit has it.
type baseDevCommand struct{ bases ports.BaseLog }

func (b baseDevCommand) DevCommand(ctx context.Context, job domain.JobID, wt ports.Worktree) (ports.DevCommand, error) {
	base, err := b.bases.Base(ctx, job)
	if err != nil {
		return ports.DevCommand{}, err
	}
	data, ok, err := gitdiff.Differ{}.AtBaseIfAny(ctx, wt.Path, base, DevServerFile)
	if err != nil || !ok {
		return ports.DevCommand{}, err
	}
	d, err := configyaml.LoadDevServer([]byte(data))
	if err != nil {
		return ports.DevCommand{}, fmt.Errorf("%s at %s: %w", DevServerFile, base, err)
	}
	return ports.DevCommand{Args: d.Command, Env: d.Env}, nil
}

// DevServerSubscriber is the name the bus keeps dev server stops under.
const DevServerSubscriber = "worker.dev-servers"

// Subscribe has b hand the job events that end sessions to the Worker d
// was given to, which stops their dev servers: after an AttemptFailed, and
// once the job leaves the stage, closes, is stopped or dropped. Call it
// after Worker and before b starts.
func (d *DevServers) Subscribe(b *bus.Bus) error {
	if d.sub == nil {
		return errNoWorker
	}
	return b.Subscribe(DevServerSubscriber, d.sub.StopLeft, worker.DevServerEvents...)
}

// errNoWorker means Subscribe ran before Worker was given d.
var errNoWorker = errors.New("dev servers: subscribe before wiring.Worker")
