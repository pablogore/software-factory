package ports

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
)

// DevServers runs the dev servers a job's stages start, so none outlives the
// stage or the job it was started for, children and grandchildren included.
type DevServers interface {
	// Start runs command in the job's worktree wt for stage, with port, one
	// of the job's ports, in its PORT environment variable. A stage runs at
	// most one dev server at a time.
	Start(ctx context.Context, wt Worktree, stage string, port int, command []string) error
	// StopStage stops the dev server of the job's stage and every process
	// it started. A server that does not exit on its own within the grace
	// period is killed with them. Stopping a stage with none is harmless.
	StopStage(ctx context.Context, job domain.JobID, stage string) error
	// StopJob is StopStage for every stage of the job still running a dev
	// server.
	StopJob(ctx context.Context, job domain.JobID) error
}
