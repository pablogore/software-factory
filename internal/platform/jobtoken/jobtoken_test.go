package jobtoken_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

var builder = jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild}

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

			s.It("refuses any operation other than submitting the result, on its own job too", func(ctx *specs.Context) {
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
			{"with a bad role name", jobtoken.Grant{Job: "j-1", Role: "Builder", Kind: domain.KindBuild}, domain.ErrInvalidValue},
			{"with an unknown kind", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: "deploy"}, domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r badGrantRow) string { return "issue no token for a grant " + r.name }, func(ctx *specs.Context, r badGrantRow) {
			token, err := jobtoken.New().Issue(r.grant)
			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(token).To(specs.BeEmpty())
		})
	})
}
