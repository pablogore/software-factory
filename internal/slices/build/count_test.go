package build_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/build"
)

var countCommand = []string{"go", "test", "-list", ".", "./..."}

// counter answers the checks command with checks and the count command with
// the run counts has for the directory it runs in, or with err; it records
// where each count ran.
type counter struct {
	checks domain.ChecksRun
	counts map[string]domain.ChecksRun
	err    error
	dirs   []string
}

func (c *counter) Run(_ context.Context, _ domain.JobID, dir string, command []string) (domain.ChecksRun, error) {
	if !slices.Equal(command, countCommand) {
		return c.checks, nil
	}
	c.dirs = append(c.dirs, dir)
	return c.counts[dir], c.err
}

// snapshots checks every commit out at /snap/<commit>, or fails with err, and
// records what it removed.
type snapshots struct {
	err     error
	removed []string
}

func (s *snapshots) Checkout(_ context.Context, _, commit string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "/snap/" + commit, nil
}

func (s *snapshots) Remove(_ context.Context, _, path string) error {
	s.removed = append(s.removed, path)
	return nil
}

// repoConfig answers with command, or err, and records the dir and base it
// was asked at.
type repoConfig struct {
	command []string
	err     error
	asked   []string
}

func (r *repoConfig) CountCommand(_ context.Context, dir, base string) ([]string, error) {
	r.asked = append(r.asked, dir+"@"+base)
	return r.command, r.err
}

func listed(output string) domain.ChecksRun {
	return domain.ChecksRun{Command: "go test -list . ./...", Output: output}
}

// jsonRun is go test -json output, stderr in it: in example.com/a a test
// whose two subtests pass and one is skipped, a test that fails in
// example.com/c, and example.com/b with no test files.
const jsonRun = `go: downloading example.com/x v1.0.0
{"ImportPath":"example.com/a","Action":"build-output","Output":"# example.com/a\n"}
{"Action":"start","Package":"example.com/a"}
{"Action":"run","Package":"example.com/a","Test":"TestA"}
{"Action":"output","Package":"example.com/a","Test":"TestA","Output":"=== RUN   TestA\n"}
{"Action":"pass","Package":"example.com/a","Test":"TestA/one"}
{"Action":"skip","Package":"example.com/a","Test":"TestA/skipped"}
{"Action":"pass","Package":"example.com/a","Test":"TestA/two"}
{"Action":"pass","Package":"example.com/a","Test":"TestA"}
{"Action":"pass","Package":"example.com/a"}
{"Action":"output","Package":"example.com/b","Output":"?   \texample.com/b\t[no test files]\n"}
{"Action":"skip","Package":"example.com/b"}
{"Action":"pass","Package":"example.com/c","Test":"TestC"}
{"Action":"fail","Package":"example.com/c","Test":"TestD"}
{"Action":"fail","Package":"example.com/c"}
`

const (
	baseList = "TestA\nTestB\nok  \texample.com/a\t0.1s\nTestC\nok  \texample.com/b\t(cached)\n"
	snap     = "/snap/" + recorded
)

func newCountGate(ctx *specs.Context, c *counter, snaps *snapshots) (*build.Gate, *gated) {
	g, store := newGateDiff(ctx, domain.StageGate, 1, &checks{}, &diffs{})
	g.Checks, g.CountCommand, g.Snapshots = c, countCommand, snaps
	return g, &gated{store}
}

// gated is the gate's store, read back for the last checks run.
type gated struct {
	store interface {
		History(ctx context.Context, id domain.JobID) ([]ports.Record, error)
		Load(ctx context.Context, id domain.JobID) (domain.Job, ports.Version, error)
	}
}

func (m *gated) last(ctx *specs.Context) domain.ChecksRun {
	hist, err := m.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	last, ok := build.LastChecks(hist)
	ctx.Expect(ok).To(specs.BeTrue())
	return last.Run
}

func (m *gated) stage(ctx *specs.Context) string {
	job, _, err := m.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return job.Stage
}

func TestTestCount(t *testing.T) {
	specs.Describe(t, "build.CountTests", func(s *specs.Spec) {
		s.It("counts the tests listed before each package's line, packages with none at zero", func(ctx *specs.Context) {
			out := "go: downloading example.com/x v1.0.0\nTestA\nBenchmarkB\nFuzzC\nExampleD\nok  \texample.com/a\t0.1s\n?   \texample.com/b\t[no test files]\nTestE\nok  \texample.com/c\t(cached)\n"

			counts, err := build.CountTests(out)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(counts).ToEqual(map[string]int{"example.com/a": 4, "example.com/b": 0, "example.com/c": 1})
		})

		s.It("refuses test names with no package line after them", func(ctx *specs.Context) {
			_, err := build.CountTests("TestA\nok  \texample.com/a\t0.1s\nTestB\n")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})

		s.It("counts each test go test -json saw pass, subtests included, and no skipped or failed one", func(ctx *specs.Context) {
			counts, err := build.CountTests(jsonRun)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(counts).ToEqual(map[string]int{"example.com/a": 3, "example.com/b": 0, "example.com/c": 1})
		})

		s.It("refuses a JSON line that is not a go test -json event", func(ctx *specs.Context) {
			_, err := build.CountTests(`{"Action":"pass","Package":"example.com/a","Test":"TestA"}` + "\n" + `{"Package":"example.com/a"` + "\n")

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})

	specs.Describe(t, "build.TestsDropped", func(s *specs.Spec) {
		s.It("names each package with fewer tests, a missing one as none, even when another gains more", func(ctx *specs.Context) {
			base := map[string]int{"b": 3, "a": 2, "c": 1, "d": 1}
			head := map[string]int{"a": 1, "c": 1, "d": 9, "e": 4}

			ctx.Expect(build.TestsDropped(base, head)).ToEqual([]string{
				"the package a has 1 tests, 2 at the base commit",
				"the package b has 0 tests, 3 at the base commit",
			})
		})

		s.It("is nothing when no package drops", func(ctx *specs.Context) {
			ctx.Expect(build.TestsDropped(map[string]int{"a": 2}, map[string]int{"a": 2, "b": 1})).To(specs.BeEmpty())
		})
	})

	specs.Describe(t, "build.Gate's test count", func(s *specs.Spec) {
		bg := context.Background()

		s.It("fails when a package has fewer tests at the head than in a copy of the base, naming it", func(ctx *specs.Context) {
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": listed("TestA\nok  \texample.com/a\t0.1s\nTestC\nTestD\nok  \texample.com/b\t0.1s\n"),
				snap:      listed(baseList),
			}}
			snaps := &snapshots{}
			g, store := newCountGate(ctx, c, snaps)

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.dirs).ToEqual([]string{"/wt/j-1", snap})
			ctx.Expect(snaps.removed).ToEqual([]string{snap})
			ctx.Expect(store.last(ctx).Violations).ToEqual([]string{"the package example.com/a has 1 tests, 2 at the base commit"})
			ctx.Expect(store.last(ctx).Notes).To(specs.BeEmpty())
			ctx.Expect(store.stage(ctx)).ToEqual("build")
		})

		s.It("fails when go test -json saw a subtest of a package skipped at the head, naming it", func(ctx *specs.Context) {
			skipped := strings.Replace(jsonRun, `"pass","Package":"example.com/a","Test":"TestA/two"`, `"skip","Package":"example.com/a","Test":"TestA/two"`, 1)
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": {Command: "go test -json -count=1 ./...", Output: skipped},
				snap:      {Command: "go test -json -count=1 ./...", Output: jsonRun},
			}}
			g, store := newCountGate(ctx, c, &snapshots{})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.last(ctx).Violations).ToEqual([]string{"the package example.com/a has 2 tests, 3 at the base commit"})
			ctx.Expect(store.stage(ctx)).ToEqual("build")
		})

		s.It("passes when no package drops", func(ctx *specs.Context) {
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": listed(baseList + "TestN\nok  \texample.com/n\t0.1s\n"),
				snap:      listed(baseList),
			}}
			g, store := newCountGate(ctx, c, &snapshots{})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.last(ctx).Violations).To(specs.BeEmpty())
			ctx.Expect(store.stage(ctx)).ToEqual("review")
		})

		s.It("notes that the rule did not run when no count command is configured", func(ctx *specs.Context) {
			c := &counter{checks: passing}
			g, store := newCountGate(ctx, c, &snapshots{})
			g.CountCommand = nil

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.dirs).To(specs.BeEmpty())
			ctx.Expect(store.last(ctx).Notes).ToEqual([]string{build.NoCountCommand})
			ctx.Expect(store.stage(ctx)).ToEqual("review")
		})

		s.It("notes that the rule did not run when no base commit was recorded", func(ctx *specs.Context) {
			c := &counter{checks: passing}
			g, store := newCountGate(ctx, c, &snapshots{})
			g.Bases = bases{err: ports.ErrNoBase}

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.dirs).To(specs.BeEmpty())
			ctx.Expect(store.last(ctx).Notes).ToEqual([]string{build.NoCountBase})
			ctx.Expect(store.last(ctx).Violations).ToEqual([]string{build.NoBase})
		})

		s.It("fails with the reason when the count command fails at the head", func(ctx *specs.Context) {
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": {Command: "go test -list . ./...", ExitCode: 1, Output: "FAIL\texample.com/a [build failed]\n"},
				snap:      listed(baseList),
			}}
			g, store := newCountGate(ctx, c, &snapshots{})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.last(ctx).Violations).ToEqual([]string{"the test count command `go test -list . ./...` failed at the job's head with exit 1: FAIL\texample.com/a [build failed]"})
			ctx.Expect(store.stage(ctx)).ToEqual("build")
		})

		s.It("fails with the reason when the count command fails at the base, removing its copy", func(ctx *specs.Context) {
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": listed(baseList),
				snap:      {Command: "go test -list . ./...", ExitCode: 2, Output: "no Go files\n"},
			}}
			snaps := &snapshots{}
			g, store := newCountGate(ctx, c, snaps)

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(snaps.removed).ToEqual([]string{snap})
			ctx.Expect(store.last(ctx).Violations).ToEqual([]string{"the test count command `go test -list . ./...` failed at the base commit " + recorded + " with exit 2: no Go files"})
			ctx.Expect(store.stage(ctx)).ToEqual("build")
		})

		s.It("fails with the runner's note when it cut the count's output, which can't be counted whole", func(ctx *specs.Context) {
			cut := listed("TestA\nok  \texample.com/a\t0.1s\n")
			cut.Notes = []string{"output truncated: kept the first 30 bytes, dropped the other 900"}
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": cut,
				snap:      listed(baseList),
			}}
			g, store := newCountGate(ctx, c, &snapshots{})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.last(ctx).Violations).ToEqual([]string{"the test count command `go test -list . ./...` can't be counted at the job's head: output truncated: kept the first 30 bytes, dropped the other 900"})
		})

		s.It("fails with the reason when the count's output can't be read", func(ctx *specs.Context) {
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": listed("TestA\n"),
			}}
			g, store := newCountGate(ctx, c, &snapshots{})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(store.last(ctx).Violations).To(specs.HaveLen(1))
			ctx.Expect(store.last(ctx).Violations[0]).To(specs.Contain("printed output at the job's head that can't be read"))
		})

		s.It("saves nothing when the count command cannot be run", func(ctx *specs.Context) {
			broken := errors.New("go: not found")
			g, store := newCountGate(ctx, &counter{checks: passing, err: broken}, &snapshots{})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(broken))
			ctx.Expect(store.stage(ctx)).ToEqual(domain.StageGate)
		})

		s.It("saves nothing when the base cannot be checked out", func(ctx *specs.Context) {
			broken := errors.New("git: worktree add failed")
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{"/wt/j-1": listed(baseList)}}
			g, store := newCountGate(ctx, c, &snapshots{err: broken})

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(broken))
			ctx.Expect(store.stage(ctx)).ToEqual(domain.StageGate)
		})

		s.It("counts with the command the repo configures at the recorded base when the operator gives none", func(ctx *specs.Context) {
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": listed("TestA\nok  \texample.com/a\t0.1s\nok  \texample.com/b\t0.1s\n"),
				snap:      listed(baseList),
			}}
			g, store := newCountGate(ctx, c, &snapshots{})
			conf := &repoConfig{command: countCommand}
			g.CountCommand, g.RepoConfig = nil, conf

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(conf.asked).ToEqual([]string{"/wt/j-1@" + recorded})
			ctx.Expect(c.dirs).ToEqual([]string{"/wt/j-1", snap})
			ctx.Expect(store.last(ctx).Violations).ToEqual([]string{
				"the package example.com/a has 1 tests, 2 at the base commit",
				"the package example.com/b has 0 tests, 1 at the base commit",
			})
			ctx.Expect(store.stage(ctx)).ToEqual("build")
		})

		s.It("counts with the operator's command over the one the repo configures", func(ctx *specs.Context) {
			c := &counter{checks: passing, counts: map[string]domain.ChecksRun{
				"/wt/j-1": listed(baseList),
				snap:      listed(baseList),
			}}
			g, store := newCountGate(ctx, c, &snapshots{})
			conf := &repoConfig{command: []string{"true"}}
			g.RepoConfig = conf

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(conf.asked).To(specs.BeEmpty())
			ctx.Expect(c.dirs).ToEqual([]string{"/wt/j-1", snap})
			ctx.Expect(store.stage(ctx)).ToEqual("review")
		})

		s.It("notes that the rule did not run when the repo configures no count command either", func(ctx *specs.Context) {
			c := &counter{checks: passing}
			g, store := newCountGate(ctx, c, &snapshots{})
			g.CountCommand, g.RepoConfig = nil, &repoConfig{}

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.dirs).To(specs.BeEmpty())
			ctx.Expect(store.last(ctx).Notes).ToEqual([]string{build.NoCountCommand})
			ctx.Expect(store.stage(ctx)).ToEqual("review")
		})

		s.It("saves nothing when the repo's count command can't be read", func(ctx *specs.Context) {
			broken := errors.New("parse gates: unknown key")
			g, store := newCountGate(ctx, &counter{checks: passing}, &snapshots{})
			g.CountCommand, g.RepoConfig = nil, &repoConfig{err: broken}

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(broken))
			ctx.Expect(store.stage(ctx)).ToEqual(domain.StageGate)
		})

		s.It("refuses to run a count command with no snapshots", func(ctx *specs.Context) {
			g, _ := newCountGate(ctx, &counter{checks: passing}, nil)
			g.Snapshots = nil

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
		})
	})
}
