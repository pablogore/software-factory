package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/build"
	"github.com/pablogore/software-factory/internal/wiring"
)

const gateDemoJob domain.JobID = "gate-demo"

// dirWorkspace hands every job the same directory as its worktree, so the
// demo gates a repo a person points it at.
type dirWorkspace struct{ dir string }

func (w dirWorkspace) Prepare(_ context.Context, job domain.JobID) (ports.Worktree, error) {
	return ports.Worktree{Job: job, Path: w.dir}, nil
}

func (dirWorkspace) Clean(context.Context, domain.JobID) error {
	return errors.New("gate demo: the demo's worktree is the person's own directory")
}

// gateDemo puts one job of the demo pipeline at the gate in round 1, runs the
// gate on it with dir as its worktree and prints the checks run kept with the
// job, where the job went and what the builder of a new round gets.
func gateDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("gate-demo", flag.ContinueOnError)
	dir := fs.String("dir", ".", "the repo to run the checks in, as the job's worktree")
	command := fs.String("checks", strings.Join(build.DefaultCommand, " "), "the checks command, split on spaces and run without a shell")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	store, err := memstore.New()
	if err != nil {
		return err
	}
	job := domain.Job{ID: gateDemoJob, Title: "Gate demo job", Pipeline: "demo", Stage: domain.StageGate, Round: 1}
	if err := store.Create(ctx, job); err != nil {
		return err
	}
	g := wiring.Gate(store, demoPipelines{}, dirWorkspace{abs}, wiring.SystemClock{}, strings.Fields(*command))
	_, _ = fmt.Fprintf(out, "job %s at the gate in round %d; running `%s` in %s\n", job.ID, job.Round, strings.Join(g.Command, " "), abs)
	if _, err := g.Run(ctx, job.ID); err != nil {
		return err
	}
	job, _, err = store.Load(ctx, job.ID)
	if err != nil {
		return err
	}
	hist, err := store.History(ctx, job.ID)
	if err != nil {
		return err
	}
	ran, _ := build.LastChecks(hist)
	_, _ = fmt.Fprintf(out, "checks kept with the job: round %d, `%s`, exit %d in %s\n--- output ---\n%s--- end of output ---\n",
		ran.Round, ran.Run.Command, ran.Run.ExitCode, ran.Run.Duration.Round(time.Millisecond), ran.Run.Output)
	evNames := make([]string, len(hist))
	for i, h := range hist {
		evNames[i] = h.Event.EventName()
	}
	_, _ = fmt.Fprintf(out, "job %s: stage %s, round %d, events [%s]\n", job.ID, job.Stage, job.Round, strings.Join(evNames, ", "))
	if job.Stage == "review" {
		_, _ = fmt.Fprintln(out, "the checks passed: the job is in review for its reviewers")
		return nil
	}
	_, _ = fmt.Fprintf(out, "the checks failed: no reviewer started; the builder of round %d gets the log of round %d above (exit %d)\n", job.Round, ran.Round, ran.Run.ExitCode)
	return nil
}
