package build_test

import (
	"context"
	"errors"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
)

func TestGateCheck(t *testing.T) {
	specs.Describe(t, "build.Gate.Check", func(s *specs.Spec) {
		bg := context.Background()

		s.It("runs the gate's command in the worktree of a job still in build and returns the run", func(ctx *specs.Context) {
			c := &checks{run: failing}
			g, _ := newGate(ctx, "build", 1, c)
			g.Command = []string{"go", "test", "./..."}

			run, err := g.Check(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(c.dir).ToEqual("/wt/j-1")
			ctx.Expect(c.command).ToEqual([]string{"go", "test", "./..."})
			ctx.Expect(run).ToEqual(noted(failing))
		})

		s.It("judges the diff by the same rules as the gate", func(ctx *specs.Context) {
			d := &diffs{changes: []domain.FileChange{{Path: "Makefile", Kind: domain.ChangeModified}}}
			g, _ := newGateDiff(ctx, "build", 1, &checks{run: passing}, d)

			run, err := g.Check(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Passed()).To(specs.BeFalse())
			ctx.Expect(run.Violations).To(specs.HaveLen(1))
			ctx.Expect(run.Violations[0]).To(specs.Contain("Makefile"))
			ctx.Expect(d.base).ToEqual(recorded)
		})

		s.It("decides and saves nothing, so the job stays where it is", func(ctx *specs.Context) {
			g, store := newGate(ctx, "build", 1, &checks{run: passing})

			_, err := g.Check(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			hist, err := store.History(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.BeEmpty())
			job, _, err := store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual("build")
		})

		s.It("is an error when the checks cannot be run", func(ctx *specs.Context) {
			boom := errors.New("no make")
			g, _ := newGate(ctx, "build", 1, &checks{err: boom})

			_, err := g.Check(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(boom))
		})

		s.It("refuses to run without a checks command", func(ctx *specs.Context) {
			c := &checks{run: passing}
			g, _ := newGate(ctx, "build", 1, c)
			g.Command = nil

			_, err := g.Check(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(c.calls).ToEqual(0)
		})

		s.It("is an error for a job that does not exist", func(ctx *specs.Context) {
			g, _ := newGate(ctx, "build", 1, &checks{run: passing})
			_, err := g.Check(bg, "j-404")
			ctx.Expect(err).To(specs.MatchError(ports.ErrJobNotFound))
		})
	})
}
