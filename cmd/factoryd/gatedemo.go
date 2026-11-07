package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
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

// rolePipelines is the demo pipeline with role, when it has a name, as its
// build stage's only role, as loaded from a role file.
type rolePipelines struct{ role domain.Role }

func (r rolePipelines) Pipeline(ctx context.Context, name string) (domain.Pipeline, error) {
	p, err := demoPipelines{}.Pipeline(ctx, name)
	if err != nil || r.role.Name == "" {
		return p, err
	}
	p.Stages = slices.Clone(p.Stages)
	for i, st := range p.Stages {
		if st.Kind == domain.KindBuild {
			p.Stages[i].Roles = []string{r.role.Name}
		}
	}
	p.Roles = append(p.Roles, r.role)
	return p, nil
}

// gateDemo puts one job of the demo pipeline at the gate in round 1, runs the
// gate on it with dir as its worktree and prints the checks run kept with the
// job, where the job went and what the builder of a new round gets. A build
// role file given with -role, such as one declaring allowed_paths, is the
// build stage's role.
func gateDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("gate-demo", flag.ContinueOnError)
	dir := fs.String("dir", ".", "the repo to run the checks in, as the job's worktree")
	command := fs.String("checks", strings.Join(build.DefaultCommand, " "), "the checks command, split on spaces and run without a shell")
	base := fs.String("base", "HEAD", "the base the job's diff is taken against; HEAD judges only uncommitted changes")
	roleFile := fs.String("role", "", "a build role file (roles/<name>.yaml) for the build stage, e.g. one with allowed_paths")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var role domain.Role
	if *roleFile != "" {
		data, err := os.ReadFile(*roleFile)
		if err != nil {
			return err
		}
		if role, err = configyaml.LoadRole(data); err != nil {
			return fmt.Errorf("%s: %w", *roleFile, err)
		}
		if role.Kind != domain.KindBuild {
			return fmt.Errorf("%s: %w: kind %s, want build", *roleFile, domain.ErrInvalidValue, role.Kind)
		}
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
	g := wiring.Gate(store, rolePipelines{role}, dirWorkspace{abs}, wiring.SystemClock{}, strings.Fields(*command), *base)
	_, _ = fmt.Fprintf(out, "job %s at the gate in round %d; running `%s` in %s and judging its diff against %s\n", job.ID, job.Round, strings.Join(g.Command, " "), abs, g.Base)
	if len(role.AllowedPaths) > 0 {
		_, _ = fmt.Fprintf(out, "build role %s may touch only [%s]\n", role.Name, strings.Join(role.AllowedPaths, ", "))
	}
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
	for _, v := range ran.Run.Violations {
		_, _ = fmt.Fprintf(out, "rule broken: %s\n", v)
	}
	evNames := make([]string, len(hist))
	for i, h := range hist {
		evNames[i] = h.Event.EventName()
	}
	_, _ = fmt.Fprintf(out, "job %s: stage %s, round %d, events [%s]\n", job.ID, job.Stage, job.Round, strings.Join(evNames, ", "))
	if job.Stage == "review" {
		_, _ = fmt.Fprintln(out, "the gate passed: the job is in review for its reviewers")
		return nil
	}
	_, _ = fmt.Fprintf(out, "the gate failed: no reviewer started; the builder of round %d gets the log and the broken rules of round %d above (exit %d)\n", job.Round, ran.Round, ran.Run.ExitCode)
	return nil
}
