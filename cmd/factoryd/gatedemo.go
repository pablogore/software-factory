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
	"github.com/pablogore/software-factory/internal/adapters/out/gitsafe"
	"github.com/pablogore/software-factory/internal/adapters/out/gitworktree"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
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

// gateStore keeps the demo job, its history and its base commit.
type gateStore interface {
	ports.JobStore
	ports.EventLog
	ports.BaseLog
}

// gateDemo puts one job of the demo pipeline at the gate in round 1, runs the
// gate on it and prints the checks run kept with the job, where the job went
// and what the builder of a new round gets. A build role file given with
// -role, such as one declaring allowed_paths, is the build stage's role.
// With -count, or with none and a count command in .factory/gates.yaml as the
// base commit has it, the gate also fails a package with fewer tests than at
// the base commit. A check running past -timeout is stopped with its
// process group and fails the gate, its run noting it timed out.
//
// With no -db the job is made in memory with dir as its worktree, recording
// the commit base is at as it starts. With -db the job is kept in that SQLite
// file: the first run makes its worktree in the repo at dir off base, as
// factoryd does, and stops so a person can work in it, move base and run it
// again to gate it. Gating a job's worktree again with no -db and -base its
// base branch as it is now is the gate a merge needs: the diff and the repo's
// gate config are those of the base the job merges into, not the one it left
// from.
func gateDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("gate-demo", flag.ContinueOnError)
	dir := fs.String("dir", ".", "the repo to run the checks in, as the job's worktree; with -db, the repo the job's worktree is made in")
	command := fs.String("checks", strings.Join(build.DefaultCommand, " "), "the checks command, split on spaces and run without a shell")
	count := fs.String("count", "", "the test count command, such as go test -json -count=1 ./... (or go test -list . ./..., which sees no subtests), split and run like -checks in the worktree and in a copy of the base commit; with none the one .factory/gates.yaml sets at the base commit, and with neither the rule doesn't run")
	base := fs.String("base", "HEAD", "the base whose commit, recorded when the job is made, the job's diff is taken against; HEAD judges only uncommitted changes")
	roleFile := fs.String("role", "", "a build role file (roles/<name>.yaml) for the build stage, e.g. one with allowed_paths")
	timeout := fs.Duration("timeout", wiring.DefaultGateTimeout, "how long each command of the gate may run before it is stopped with its process group and the gate fails, saying it timed out")
	db := fs.String("db", "", "a SQLite file keeping the job between runs: the first makes its worktree in -dir off -base, the next gates it")
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
	homes, err := childenv.DefaultHomes()
	if err != nil {
		return err
	}
	job := domain.Job{ID: gateDemoJob, Title: "Gate demo job", Pipeline: "demo", Stage: domain.StageGate, Round: 1}
	var store gateStore
	var ws ports.Workspace
	if *db == "" {
		mem, err := memstore.New()
		if err != nil {
			return err
		}
		if err := mem.Create(ctx, job); err != nil {
			return err
		}
		commit, err := revParse(ctx, homes, gateDemoJob, abs, *base)
		if err != nil {
			return err
		}
		if err := mem.RecordBase(ctx, job.ID, commit); err != nil {
			return err
		}
		store, ws = mem, dirWorkspace{abs}
	} else {
		lite, err := sqlite.Open(ctx, *db)
		if err != nil {
			return err
		}
		defer func() { _ = lite.Close() }()
		wts := gitworktree.New(abs, *base, lite, homes)
		saved, _, err := lite.Load(ctx, job.ID)
		if errors.Is(err, ports.ErrJobNotFound) {
			return makeDemoJob(ctx, lite, wts, job, *base, *dir, *db, out)
		}
		if err != nil {
			return err
		}
		job, store, ws = saved, lite, wts
	}
	wt, err := ws.Prepare(ctx, job.ID)
	if err != nil {
		return err
	}
	commit, err := store.Base(ctx, job.ID)
	if errors.Is(err, ports.ErrNoBase) {
		commit = "none"
	} else if err != nil {
		return err
	}
	g := wiring.Gate(store, rolePipelines{role}, ws, store, wiring.SystemClock{}, gatelock.New(), homes, strings.Fields(*command), strings.Fields(*count), *timeout)
	_, _ = fmt.Fprintf(out, "job %s at the gate in round %d; running `%s` in %s and judging its diff against the base commit recorded for it: %s\n", job.ID, job.Round, strings.Join(g.Command, " "), wt.Path, commit)
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
	for _, n := range ran.Run.Notes {
		_, _ = fmt.Fprintf(out, "note: %s\n", n)
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

// makeDemoJob saves job at the gate and makes its worktree off base, which
// records the commit base is at, and tells the person what to do next.
func makeDemoJob(ctx context.Context, store gateStore, ws ports.Workspace, job domain.Job, base, dir, db string, out io.Writer) error {
	if err := store.Create(ctx, job); err != nil {
		return err
	}
	wt, err := ws.Prepare(ctx, job.ID)
	if err != nil {
		return err
	}
	commit, err := store.Base(ctx, job.ID)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "job %s made: worktree %s on branch %s off %s, base commit %s recorded in %s\n", job.ID, wt.Path, wt.Branch, base, commit, db)
	_, _ = fmt.Fprintf(out, "commit the job's work in the worktree, then run gate-demo again with -dir %s -db %s to gate it\n", dir, db)
	return nil
}

// revParse is the full object name of the commit rev names in the repo at
// dir, read with git run for job with its HOME in homes, as the gate runs
// git on the job's worktree: dir is the person's, but the job's checks run
// there and can write its config.
func revParse(ctx context.Context, homes childenv.Homes, job domain.JobID, dir, rev string) (string, error) {
	home, err := homes.For(job)
	if err != nil {
		return "", err
	}
	out, err := gitsafe.Run(ctx, home, dir, nil, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("base %q: %w", rev, err)
	}
	return strings.TrimSpace(out), nil
}
