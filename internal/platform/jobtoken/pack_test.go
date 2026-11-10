package jobtoken_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

func TestContextPackTokens(t *testing.T) {
	specs.Describe(t, "job tokens and context_pack", func(s *specs.Spec) {
		s.It("authorize reading the pack of their own job for a role of any kind that lists it", func(ctx *specs.Context) {
			tokens := jobtoken.New()
			for _, kind := range domain.Kinds {
				g := jobtoken.Grant{Job: "j-1", Role: "reader", Kind: kind, Tools: []string{"context_pack"}, Round: 1}
				got, err := tokens.Authorize(issue(ctx, tokens, g), jobtoken.OpContextPack, "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(got).ToEqual(g)
			}
		})

		s.It("refuse a role that lists other tools, naming the role and the tool", func(ctx *specs.Context) {
			tokens := jobtoken.New()
			_, err := tokens.Authorize(issue(ctx, tokens, submitsOnly), jobtoken.OpContextPack, "j-1")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(err.Error()).ToEqual(`job token not allowed: role "only-submits" may not use tool "context_pack"`)
		})

		s.It("refuse the pack of another job", func(ctx *specs.Context) {
			tokens := jobtoken.New()
			g := jobtoken.Grant{Job: "j-2", Role: "builder", Kind: domain.KindBuild, Tools: []string{"context_pack"}, Round: 1}
			_, err := tokens.Authorize(issue(ctx, tokens, g), jobtoken.OpContextPack, "j-1")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
		})
	})
}
