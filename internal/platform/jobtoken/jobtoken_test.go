package jobtoken_test

import (
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

var builder = jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1}

var builderRole = domain.Role{Name: "builder", Kind: domain.KindBuild}

// submitsOnly is a grant whose role lists submit_result as its only tool.
var submitsOnly = jobtoken.Grant{Job: "j-1", Role: "only-submits", Kind: domain.KindBuild, Tools: []string{"submit_result"}, Round: 1}

// history is a job's history of events evs.
func history(evs ...domain.Event) []ports.Record {
	hist := make([]ports.Record, len(evs))
	for i, ev := range evs {
		hist[i] = ports.Record{Event: ev, At: time.Time{}}
	}
	return hist
}

func entered(stage string) domain.Event { return domain.StageEntered{Job: "j-1", Stage: stage} }

func issue(ctx *specs.Context, t *jobtoken.Tokens, g jobtoken.Grant) string {
	token, err := t.Issue(g)
	ctx.Expect(err).To(specs.BeNil())
	return token
}

type badGrantRow struct {
	name  string
	grant jobtoken.Grant
	want  error
}

func TestTokens(t *testing.T) {
	specs.Describe(t, "job tokens", func(s *specs.Spec) {
		s.It("authorize submitting the result of their own job, with its role and kind", func(ctx *specs.Context) {
			tokens := jobtoken.New()
			g, err := tokens.Authorize(issue(ctx, tokens, builder), jobtoken.OpSubmitResult, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(g).ToEqual(builder)
		})

		s.It("authorize reporting progress on their own job, with its role", func(ctx *specs.Context) {
			tokens := jobtoken.New()
			g, err := tokens.Authorize(issue(ctx, tokens, builder), jobtoken.OpProgress, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(g).ToEqual(builder)
		})

		s.When("the role lists its tools", func(s *specs.Spec) {
			s.It("authorize the operation of a listed tool", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				g, err := tokens.Authorize(issue(ctx, tokens, submitsOnly), jobtoken.OpSubmitResult, "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(g).ToEqual(submitsOnly)
			})

			s.It("refuse the operation of a tool the role does not list, naming the tool and the role", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				_, err := tokens.Authorize(issue(ctx, tokens, submitsOnly), jobtoken.OpProgress, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				ctx.Expect(err.Error()).ToEqual(`job token not allowed: role "only-submits" may not use tool "progress"`)
			})

			s.It("still refuse another job before looking at the tools", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				_, err := tokens.Authorize(issue(ctx, tokens, submitsOnly), jobtoken.OpProgress, "j-2")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				ctx.Expect(err.Error()).To(specs.Contain(`job "j-2"`))
			})

			s.It("are granted the role's tools, a copy the role's later changes do not reach", func(ctx *specs.Context) {
				role := domain.Role{Name: "only-submits", Kind: domain.KindBuild, Tools: []string{"submit_result"}}
				g := jobtoken.For(domain.Job{ID: "j-1", Stage: "build", Round: 1}, nil, role)
				role.Tools[0] = "progress"
				ctx.Expect(g).ToEqual(jobtoken.Grant{Job: "j-1", Role: "only-submits", Kind: domain.KindBuild, Tools: []string{"submit_result"}, Round: 1})
			})
		})

		s.When("asked to run the job's checks", func(s *specs.Spec) {
			gating := jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress", "run_gate"}, Round: 1}

			s.It("authorize a build role that lists run_gate", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				g, err := tokens.Authorize(issue(ctx, tokens, gating), jobtoken.OpRunGate, "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(g).ToEqual(gating)
			})

			s.It("refuse a role without a tools list, as run_gate is not one of the tools every role has", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				_, err := tokens.Authorize(issue(ctx, tokens, builder), jobtoken.OpRunGate, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				ctx.Expect(err.Error()).ToEqual(`job token not allowed: role "builder" may not use tool "run_gate"`)
			})

			s.It("refuse a role whose tools do not list run_gate", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				_, err := tokens.Authorize(issue(ctx, tokens, submitsOnly), jobtoken.OpRunGate, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			})

			s.It("refuse a role of another kind, even one that lists run_gate", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				reviewer := jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Tools: []string{"run_gate"}, Round: 1}
				_, err := tokens.Authorize(issue(ctx, tokens, reviewer), jobtoken.OpRunGate, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				ctx.Expect(err.Error()).ToEqual(`job token not allowed: role "reviewer-code" may not use tool "run_gate"`)
			})

			s.It("refuse another job's checks", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				_, err := tokens.Authorize(issue(ctx, tokens, gating), jobtoken.OpRunGate, "j-2")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			})
		})

		s.It("are different on every issue, even for the same grant", func(ctx *specs.Context) {
			tokens := jobtoken.New()
			ctx.Expect(issue(ctx, tokens, builder)).To(specs.Not(specs.Equal(issue(ctx, tokens, builder))))
		})

		s.When("the token is not for this call", func(s *specs.Spec) {
			s.It("refuses another job's result", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				_, err := tokens.Authorize(issue(ctx, tokens, builder), jobtoken.OpSubmitResult, "j-2")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			})

			s.It("refuses progress on another job", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				_, err := tokens.Authorize(issue(ctx, tokens, builder), jobtoken.OpProgress, "j-2")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			})

			s.It("refuses any operation other than submitting the result or reporting progress, on its own job too", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				token := issue(ctx, tokens, builder)
				for _, op := range []jobtoken.Op{"job.approve", "job.merge", "config.apply", ""} {
					_, err := tokens.Authorize(token, op, "j-1")
					ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				}
			})

			s.It("refuses a call without a token", func(ctx *specs.Context) {
				_, err := jobtoken.New().Authorize("", jobtoken.OpSubmitResult, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrNoToken))
			})

			s.It("refuses a token it never issued", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				issue(ctx, tokens, builder)
				other := issue(ctx, jobtoken.New(), builder)
				_, err := tokens.Authorize(other, jobtoken.OpSubmitResult, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
			})

			s.It("refuses a revoked token", func(ctx *specs.Context) {
				tokens := jobtoken.New()
				token := issue(ctx, tokens, builder)
				tokens.Revoke(token)
				_, err := tokens.Authorize(token, jobtoken.OpSubmitResult, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
			})
		})

		rows := []badGrantRow{
			{"without a job", jobtoken.Grant{Role: "builder", Kind: domain.KindBuild}, domain.ErrMissingField},
			{"with a bad role name", jobtoken.Grant{Job: "j-1", Role: "Builder", Kind: domain.KindBuild, Round: 1}, domain.ErrInvalidValue},
			{"with an unknown kind", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: "deploy", Round: 1}, domain.ErrInvalidValue},
			{"without a round", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild}, domain.ErrInvalidValue},
			{"with a negative entry", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1, Entry: -1}, domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r badGrantRow) string { return "issue no token for a grant " + r.name }, func(ctx *specs.Context, r badGrantRow) {
			token, err := jobtoken.New().Issue(r.grant)
			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(token).To(specs.BeEmpty())
		})

		s.When("checking the job is still where the token was issued", func(s *specs.Spec) {
			job := domain.Job{ID: "j-1", Stage: "build", Round: 1}
			start := history(entered("build"))

			s.It("grants the job's round and its stage entry as it is now", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, builderRole)
				ctx.Expect(g).ToEqual(jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1, Entry: 1})
				ctx.Expect(g.Current(job, start)).To(specs.BeNil())
			})

			s.It("takes events that leave the job in its stage", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, domain.Role{Name: "reviewer-code", Kind: domain.KindReview})
				later := history(entered("build"), domain.ReviewPassed{Job: "j-1", Role: "reviewer-security"})
				ctx.Expect(g.Current(job, later)).To(specs.BeNil())
			})

			s.It("refuses once the job is in a later round", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, builderRole)
				later := domain.Job{ID: "j-1", Stage: "build", Round: 2}
				err := g.Current(later, start)
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
				ctx.Expect(err.Error()).To(specs.Contain("issued in round 1"))
			})

			s.It("refuses once the job left the stage and came back to it, in the same round", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, builderRole)
				back := history(entered("build"), domain.NeedsInfo{Job: "j-1", From: "build"}, entered("build"))
				ctx.Expect(g.Current(job, back)).To(specs.MatchError(jobtoken.ErrStale))
			})

			s.It("refuses once the job entered its own stage again", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, builderRole)
				ctx.Expect(g.Current(job, history(entered("build"), entered("build")))).To(specs.MatchError(jobtoken.ErrStale))
			})
		})
	})
}
