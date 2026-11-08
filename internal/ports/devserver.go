package ports

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
)

// DevCommand is how a repo starts its dev server: Args, run without a
// shell, and Env, the variables it sets for it besides the few every
// process needs.
type DevCommand struct {
	Args []string
	Env  map[string]string
}

// DevServers runs the dev servers a job's stages start, so none outlives the
// stage or the job it was started for, children and grandchildren included.
type DevServers interface {
	// Start runs cmd in the job's worktree wt for stage, with port, one of
	// the job's ports, in its PORT environment variable. Its environment is
	// only PATH, HOME, LANG and TMPDIR from factoryd's, cmd.Env and PORT, so
	// no credential factoryd holds reaches code a job's branch wrote. A
	// stage runs at most one dev server at a time.
	Start(ctx context.Context, wt Worktree, stage string, port int, cmd DevCommand) error
	// StopStage stops the dev server of the job's stage and every process
	// it started. A server that does not exit on its own within the grace
	// period is killed with them. Stopping a stage with none is harmless.
	StopStage(ctx context.Context, job domain.JobID, stage string) error
	// StopJob is StopStage for every stage of the job still running a dev
	// server.
	StopJob(ctx context.Context, job domain.JobID) error
}
