//go:build unix

package wiring_test

import (
	"context"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/wiring"
)

// ratingRoles are review roles as their files declare them: one with no
// when, one kept for high risk, one kept for the auth surface and one kept
// for high-risk work on auth.
var ratingRoles = []string{
	"name: reviewer-code\nkind: review\nmode: blocking\n",
	"name: reviewer-risky\nkind: review\nmode: blocking\nwhen:\n  any:\n    - risk: [high]\n",
	"name: reviewer-auth\nkind: review\nmode: blocking\nwhen:\n  any:\n    - surfaces: [auth, payments]\n",
	"name: reviewer-risky-auth\nkind: review\nmode: blocking\nwhen:\n  all:\n    - risk: [high]\n    - surfaces: [auth]\n",
}

type ratingDemoRow struct {
	name     string
	risk     domain.Risk
	surfaces []string
	skipped  []domain.ReviewSkipped
}

func TestDemoReviewSkipByIntake(t *testing.T) {
	specs.Describe(t, "a review round on intake's rating of the job", func(s *specs.Spec) {
		bg := context.Background()
		at := fixedClock{}.Now()
		rows := []ratingDemoRow{
			{"runs every role on a high-risk job that touches auth", domain.RiskHigh, []string{"auth"}, nil},
			{"skips the high-risk and auth roles, with their reasons, on a low-risk job with no surface", domain.RiskLow, []string{}, []domain.ReviewSkipped{
				{Job: "j-1", Round: 1, Role: "reviewer-risky", Reason: "when does not apply: the intake risk is low, not high", At: at},
				{Job: "j-1", Round: 1, Role: "reviewer-auth", Reason: "when does not apply: no intake surface matches surfaces auth, payments", At: at},
				{Job: "j-1", Round: 1, Role: "reviewer-risky-auth", Reason: "when does not apply: the intake risk is low, not high; no intake surface matches surfaces auth", At: at},
			}},
			{"skips the auth roles on a high-risk job that touches the ui", domain.RiskHigh, []string{"ui"}, []domain.ReviewSkipped{
				{Job: "j-1", Round: 1, Role: "reviewer-auth", Reason: "when does not apply: no intake surface matches surfaces auth, payments", At: at},
				{Job: "j-1", Round: 1, Role: "reviewer-risky-auth", Reason: "when does not apply: no intake surface matches surfaces auth", At: at},
			}},
			{"skips only the high-risk roles on a low-risk job that touches auth", domain.RiskLow, []string{"auth"}, []domain.ReviewSkipped{
				{Job: "j-1", Round: 1, Role: "reviewer-risky", Reason: "when does not apply: the intake risk is low, not high", At: at},
				{Job: "j-1", Round: 1, Role: "reviewer-risky-auth", Reason: "when does not apply: the intake risk is low, not high", At: at},
			}},
			{"runs the high-risk roles on a job intake never rated", "", nil, nil},
		}
		specs.Table(s, rows, func(r ratingDemoRow) string { return r.name }, func(ctx *specs.Context, r ratingDemoRow) {
			var roles []domain.Role
			for _, f := range ratingRoles {
				role, err := configyaml.LoadRole([]byte(f))
				ctx.Expect(err).To(specs.BeNil())
				roles = append(roles, role)
			}
			p := skipPipeline{roles: roles}
			dir, base := changedRepo(ctx)
			store, err := memstore.New()
			ctx.Expect(err).To(specs.BeNil())
			job := domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: domain.StageGate, Round: 1, Risk: r.risk, Surfaces: r.surfaces}
			ctx.Expect(store.Create(bg, job)).To(specs.BeNil())
			ctx.Expect(store.RecordBase(bg, "j-1", base)).To(specs.BeNil())
			homes := childenv.Homes{Root: ctx.Testing().TempDir()}

			g := wiring.Gate(store, p, oneDir(dir), store, fixedClock{}, gatelock.New(), homes, []string{"true"}, nil, 0)
			_, err = g.Run(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())

			hist, err := store.History(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			var skips []domain.ReviewSkipped
			for _, rec := range hist {
				if e, ok := rec.Event.(domain.ReviewSkipped); ok {
					skips = append(skips, e)
				}
			}
			// Printed so `go test -v -run TestDemo` shows which roles ran.
			ctx.Testing().Logf("risk %q, surfaces %v: skipped %v", r.risk, r.surfaces, skips)
			ctx.Expect(skips).ToEqual(r.skipped)
			loaded, _, err := store.Load(bg, "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(loaded.Stage).ToEqual("review")
		})
	})
}
