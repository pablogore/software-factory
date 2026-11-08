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
				g := jobtoken.For(job, start, "builder", domain.KindBuild)
				ctx.Expect(g).ToEqual(jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1, Entry: 1})
				ctx.Expect(g.Current(job, start)).To(specs.BeNil())
			})

			s.It("takes events that leave the job in its stage", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, "reviewer-code", domain.KindReview)
				later := history(entered("build"), domain.ReviewPassed{Job: "j-1", Role: "reviewer-security"})
				ctx.Expect(g.Current(job, later)).To(specs.BeNil())
			})

			s.It("refuses once the job is in a later round", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, "builder", domain.KindBuild)
				later := domain.Job{ID: "j-1", Stage: "build", Round: 2}
				err := g.Current(later, start)
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
				ctx.Expect(err.Error()).To(specs.Contain("issued in round 1"))
			})

			s.It("refuses once the job left the stage and came back to it, in the same round", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, "builder", domain.KindBuild)
				back := history(entered("build"), domain.NeedsInfo{Job: "j-1", From: "build"}, entered("build"))
				ctx.Expect(g.Current(job, back)).To(specs.MatchError(jobtoken.ErrStale))
			})

			s.It("refuses once the job entered its own stage again", func(ctx *specs.Context) {
				g := jobtoken.For(job, start, "builder", domain.KindBuild)
				ctx.Expect(g.Current(job, history(entered("build"), entered("build")))).To(specs.MatchError(jobtoken.ErrStale))
			})
		})
	})
}
