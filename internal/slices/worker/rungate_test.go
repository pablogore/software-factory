package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// gate answers every check with run, or with err, and records the jobs it
// was asked to check.
type gate struct {
	run  domain.ChecksRun
	err  error
	jobs []domain.JobID
}

func (g *gate) Check(_ context.Context, id domain.JobID) (domain.ChecksRun, error) {
	g.jobs = append(g.jobs, id)
	return g.run, g.err
}

var gated = domain.ChecksRun{Command: "make check", ExitCode: 2, Output: "--- FAIL: TestSave", Violations: []string{"Makefile is protected"}}

// gating is the tools of a build role that may run the job's checks.
var gating = []string{"submit_result", "progress", "run_gate"}

// withGate is w with a gate answering run on its submitter.
func withGate(w world, run domain.ChecksRun) (world, *gate) {
	g := &gate{run: run}
	w.sub.Gate = g
	return w, g
}

type refusedGateRow struct {
	name  string
	grant jobtoken.Grant
}

func TestRunGate(t *testing.T) {
	specs.Describe(t, "running the job's checks with a job token", func(s *specs.Spec) {
		s.It("returns the run of its job's checks to a builder that lists run_gate, saving nothing", func(ctx *specs.Context) {
			w, g := withGate(newWorld(ctx), gated)
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})

			run, err := w.sub.RunGate(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run).ToEqual(gated)
			ctx.Expect(g.jobs).ToEqual([]domain.JobID{"j-1"})
			w.untouched(ctx, "j-1")
			w.untouched(ctx, "j-2")
		})

		s.It("keeps the token, so the builder runs the checks again and still submits its result", func(ctx *specs.Context) {
			w, g := withGate(newWorld(ctx), gated)
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})

			_, first := w.sub.RunGate(context.Background(), token, "j-1")
			_, second := w.sub.RunGate(context.Background(), token, "j-1")
			_, submitted := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"status":"READY"}`))

			ctx.Expect(first).To(specs.BeNil())
			ctx.Expect(second).To(specs.BeNil())
			ctx.Expect(submitted).To(specs.BeNil())
			ctx.Expect(g.jobs).To(specs.HaveLen(2))
		})

		refused := []refusedGateRow{
			{"a builder without a tools list", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1}},
			{"a builder whose tools do not list run_gate", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress"}, Round: 1}},
			{"a reviewer without a tools list", jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Round: 1}},
			{"a reviewer that lists run_gate", jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Tools: gating, Round: 1}},
			{"an approver that lists run_gate", jobtoken.Grant{Job: "j-1", Role: "approver", Kind: domain.KindApprove, Tools: gating, Round: 1}},
		}
		specs.Table(s, refused, func(r refusedGateRow) string { return "refuses " + r.name + ", running no check" }, func(ctx *specs.Context, r refusedGateRow) {
			w, g := withGate(newWorld(ctx), gated)
			token := w.issue(ctx, r.grant)

			run, err := w.sub.RunGate(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(err.Error()).To(specs.Contain(`may not use tool "run_gate"`))
			ctx.Expect(run).ToEqual(domain.ChecksRun{})
			ctx.Expect(g.jobs).To(specs.BeEmpty())
			w.untouched(ctx, "j-1")
		})

		s.When("the token is not for this run", func(s *specs.Spec) {
			s.It("refuses another job's token, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-2", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				_, err := w.sub.RunGate(context.Background(), token, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				ctx.Expect(g.jobs).To(specs.BeEmpty())
			})

			s.It("refuses a call without a token, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				_, err := w.sub.RunGate(context.Background(), "", "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrNoToken))
				ctx.Expect(g.jobs).To(specs.BeEmpty())
			})

			s.It("refuses a token from an earlier round, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				w.newRound(ctx, "j-1")
				_, err := w.sub.RunGate(context.Background(), token, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
				ctx.Expect(g.jobs).To(specs.BeEmpty())
			})

			s.It("refuses a token whose result was saved, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				_, err := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"status":"READY"}`))
				ctx.Expect(err).To(specs.BeNil())
				_, err = w.sub.RunGate(context.Background(), token, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
				ctx.Expect(g.jobs).To(specs.BeEmpty())
			})
		})

		s.It("is an error when the checks cannot be run", func(ctx *specs.Context) {
			w, g := withGate(newWorld(ctx), gated)
			boom := errors.New("no make")
			g.err = boom
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
			_, err := w.sub.RunGate(context.Background(), token, "j-1")
			ctx.Expect(err).To(specs.MatchError(boom))
			w.untouched(ctx, "j-1")
		})

		s.It("is an error, not a panic, without a gate", func(ctx *specs.Context) {
			w := newWorld(ctx)
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
			_, err := w.sub.RunGate(context.Background(), token, "j-1")
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})
	})
}
