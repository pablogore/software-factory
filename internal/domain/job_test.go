package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
)

func TestJob(t *testing.T) {
	specs.Describe(t, "Job", func(s *specs.Spec) {
		s.It("reads back from JSON equal to the original", func(ctx *specs.Context) {
			job := domain.Job{
				ID:       "001-e01-1-modelo-de",
				Title:    "Domain model",
				Pipeline: "default",
				Stage:    "review",
				Round:    2,
				Origin:   domain.Origin{Provider: "github", Thread: "issue/23", Author: "pablogore"},
				Subscriptions: []domain.Subscription{
					{Provider: "github", Thread: "issue/23", Kind: domain.ThreadIssue},
					{Provider: "github", Thread: "pr/24", Kind: domain.ThreadPR},
					{Provider: "discord", Thread: "1234", Kind: domain.ThreadChat},
				},
			}

			data, err := json.Marshal(job)
			ctx.Expect(err).To(specs.BeNil())
			var back domain.Job
			ctx.Expect(json.Unmarshal(data, &back)).To(specs.BeNil())

			ctx.Expect(back).ToEqual(job)
		})
	})
}

func TestKind(t *testing.T) {
	specs.Describe(t, "Kind", func(s *specs.Spec) {
		s.It("accepts the six kinds", func(ctx *specs.Context) {
			for _, k := range domain.Kinds {
				ctx.Expect(k.Validate()).To(specs.BeNil())
			}
			ctx.Expect(domain.Kinds).To(specs.HaveLen(6))
		})

		s.It("rejects an unknown kind", func(ctx *specs.Context) {
			ctx.Expect(domain.Kind("deploy").Validate()).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}
