package configyaml_test

import (
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// rerunPipeline reviews with two blocking reviewers; reviewer-security's role
// file marks it always_rerun.
const rerunPipeline = `name: rerun
stages:
  - build
  - review:
      parallel: [reviewer-security, reviewer-code]
      on_changes: build
      max_rounds: 3
  - approve
`

var rerunRoleFiles = []string{
	"name: reviewer-security\nkind: review\nprompt: roles/reviewer-security.md\nmode: blocking\nalways_rerun: true\n",
	"name: reviewer-code\nkind: review\nprompt: roles/reviewer-code.md\nmode: blocking\n",
}

func TestLoadPipelineAlwaysRerun(t *testing.T) {
	specs.Describe(t, "LoadPipeline with an always_rerun reviewer", func(s *specs.Spec) {
		s.It("lists the always_rerun reviewer in round 2 although it passed in round 1", func(ctx *specs.Context) {
			roles := make([]domain.Role, 0, len(rerunRoleFiles))
			for _, data := range rerunRoleFiles {
				r, err := configyaml.LoadRole([]byte(data))
				ctx.Expect(err).To(specs.BeNil())
				roles = append(roles, r)
			}
			p, err := configyaml.LoadPipeline([]byte(rerunPipeline), roles)
			ctx.Expect(err).To(specs.BeNil())

			at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
			job := domain.Job{ID: "job-rerun", Pipeline: p.Name, Stage: "review", Round: 1}
			verdicts := []domain.ResultSubmitted{
				{Job: job.ID, Role: "reviewer-security", Result: domain.ReviewResult{Verdict: domain.ReviewPass}, At: at},
				{Job: job.ID, Role: "reviewer-code", Result: domain.ReviewResult{Verdict: domain.ReviewChanges}, At: at},
			}
			for _, ev := range verdicts {
				evs, err := decide.Decide(job, ev, p)
				ctx.Expect(err).To(specs.BeNil())
				job = decide.Apply(job, evs)
			}

			ctx.Expect(job.Stage).ToEqual("build")
			ctx.Expect(job.Round).ToEqual(2)
			ctx.Expect(job.Reviewers).ToEqual([]string{"reviewer-security", "reviewer-code"})
		})
	})
}
