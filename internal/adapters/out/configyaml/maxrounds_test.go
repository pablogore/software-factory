package configyaml_test

import (
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

type roundsRow struct {
	name  string
	yaml  string
	limit int
}

// reviewUntilNeedsHuman runs reviewer-code's CHANGES round after round and
// returns the round the job went to needs-human in. Build and gate are left
// out: only the review's verdict counts rounds.
func reviewUntilNeedsHuman(ctx *specs.Context, p domain.Pipeline) int {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	job := domain.Job{ID: "job-rounds", Pipeline: p.Name, Stage: "review", Round: 1}
	for range 10 {
		evs, err := decide.Decide(job, domain.ResultSubmitted{Job: job.ID, Role: "reviewer-code", Result: domain.ReviewResult{Verdict: domain.ReviewChanges}, At: at}, p)
		ctx.Expect(err).To(specs.BeNil())
		round := job.Round
		job = decide.Apply(job, evs)
		if job.Stage == domain.StageNeedsHuman {
			return round
		}
		ctx.Expect(job.Stage).ToEqual("build")
		ctx.Expect(job.Round).ToEqual(round + 1)
		job.Stage = "review"
	}
	return 0
}

func TestLoadPipelineMaxRounds(t *testing.T) {
	specs.Describe(t, "LoadPipeline with limits.max_rounds", func(s *specs.Spec) {
		rows := []roundsRow{
			{"a review without max_rounds follows limits.max_rounds 2",
				"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\nlimits:\n  max_rounds: 2\n", 2},
			{"a review's max_rounds 4 wins over limits.max_rounds 2",
				"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n      max_rounds: 4\n  - approve\nlimits:\n  max_rounds: 2\n", 4},
			{"a review without max_rounds and no limits allows 3 rounds",
				"name: p\nstages:\n  - build\n  - review:\n      parallel: [reviewer-code]\n      on_changes: build\n  - approve\n", 3},
		}
		specs.Table(s, rows, func(r roundsRow) string { return r.name }, func(ctx *specs.Context, r roundsRow) {
			p, err := configyaml.LoadPipeline([]byte(r.yaml), loadedRoles(ctx))
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(reviewUntilNeedsHuman(ctx, p)).ToEqual(r.limit)
		})
	})
}
