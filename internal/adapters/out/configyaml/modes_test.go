package configyaml_test

import (
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/configyaml"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// modesPipeline reviews with one reviewer of each mode.
const modesPipeline = `name: modes
stages:
  - build
  - review:
      parallel: [reviewer-shadow, reviewer-advisory, reviewer-blocking]
      on_changes: build
      max_rounds: 3
  - approve
`

// modeRoleFiles are the role files of modesPipeline, as an operator writes them.
var modeRoleFiles = []string{
	"name: reviewer-shadow\nkind: review\nprompt: roles/reviewer-shadow.md\nmode: shadow\n",
	"name: reviewer-advisory\nkind: review\nprompt: roles/reviewer-advisory.md\nmode: advisory\n",
	"name: reviewer-blocking\nkind: review\nprompt: roles/reviewer-blocking.md\nmode: blocking\n",
}

func loadModesPipeline(ctx *specs.Context) domain.Pipeline {
	roles := make([]domain.Role, 0, len(modeRoleFiles))
	for _, data := range modeRoleFiles {
		r, err := configyaml.LoadRole([]byte(data))
		ctx.Expect(err).To(specs.BeNil())
		roles = append(roles, r)
	}
	p, err := configyaml.LoadPipeline([]byte(modesPipeline), roles)
	ctx.Expect(err).To(specs.BeNil())
	return p
}

// reviewRound feeds one round-1 verdict per reviewer, in stage order, and
// returns the events Decide gives for the last one.
func reviewRound(ctx *specs.Context, p domain.Pipeline, verdicts map[string]domain.ReviewVerdict) []domain.Event {
	job := domain.Job{ID: "job-modes", Pipeline: p.Name, Stage: "review", Round: 1}
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	var evs []domain.Event
	for _, role := range p.Stages[1].Roles {
		ev := domain.ResultSubmitted{Job: job.ID, Role: role, Result: domain.ReviewResult{Verdict: verdicts[role]}, At: at}
		var err error
		evs, err = decide.Decide(job, ev, p)
		ctx.Expect(err).To(specs.BeNil())
		job = decide.Apply(job, evs)
	}
	return evs
}

type modeErrorRow struct {
	name  string
	roles []domain.Role
	want  string
}

func TestLoadPipelineModes(t *testing.T) {
	specs.Describe(t, "LoadPipeline with reviewer modes", func(s *specs.Spec) {
		s.It("gives every reviewer the mode of its role file", func(ctx *specs.Context) {
			p := loadModesPipeline(ctx)

			modes := map[string]domain.Mode{}
			for _, r := range p.Roles {
				modes[r.Name] = r.Mode
			}
			ctx.Expect(modes).ToEqual(map[string]domain.Mode{
				"reviewer-shadow":   domain.ModeShadow,
				"reviewer-advisory": domain.ModeAdvisory,
				"reviewer-blocking": domain.ModeBlocking,
			})
		})

		s.It("moves on to approve when only the shadow and advisory reviewers ask for CHANGES", func(ctx *specs.Context) {
			evs := reviewRound(ctx, loadModesPipeline(ctx), map[string]domain.ReviewVerdict{
				"reviewer-shadow":   domain.ReviewChanges,
				"reviewer-advisory": domain.ReviewChanges,
				"reviewer-blocking": domain.ReviewPass,
			})

			ctx.Expect(evs[len(evs)-1]).ToEqual(domain.StageEntered{Job: "job-modes", Stage: "approve", At: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)})
		})

		s.It("goes back to build when the blocking reviewer asks for CHANGES", func(ctx *specs.Context) {
			evs := reviewRound(ctx, loadModesPipeline(ctx), map[string]domain.ReviewVerdict{
				"reviewer-shadow":   domain.ReviewPass,
				"reviewer-advisory": domain.ReviewPass,
				"reviewer-blocking": domain.ReviewChanges,
			})

			ctx.Expect(evs[len(evs)-1]).ToEqual(domain.StageEntered{Job: "job-modes", Stage: "build", At: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)})
		})

		s.When("a reviewer's mode can't be read", func(s *specs.Spec) {
			shadow := domain.Role{Name: "reviewer-shadow", Kind: domain.KindReview, Mode: domain.ModeShadow}
			advisory := domain.Role{Name: "reviewer-advisory", Kind: domain.KindReview, Mode: domain.ModeAdvisory}
			blocking := func(m domain.Mode) domain.Role {
				return domain.Role{Name: "reviewer-blocking", Kind: domain.KindReview, Mode: m}
			}
			rows := []modeErrorRow{
				{"names the reviewer with no role file", []domain.Role{shadow, advisory},
					`stage review: invalid value: role "reviewer-blocking" is not loaded`},
				{"names the reviewer with an empty mode", []domain.Role{shadow, advisory, blocking("")},
					`role reviewer-blocking: invalid value: mode ""`},
				{"names the reviewer with an unknown mode", []domain.Role{shadow, advisory, blocking("loud")},
					`role reviewer-blocking: invalid value: mode "loud"`},
			}
			specs.Table(s, rows, func(r modeErrorRow) string {
				return r.name
			}, func(ctx *specs.Context, r modeErrorRow) {
				_, err := configyaml.LoadPipeline([]byte(modesPipeline), r.roles)

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
				ctx.Expect(err.Error()).ToEqual(r.want)
			})

			s.It("names the reviewer whose role file has no mode", func(ctx *specs.Context) {
				_, err := configyaml.LoadRole([]byte("name: reviewer-blocking\nkind: review\nprompt: roles/reviewer-blocking.md\n"))

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
				ctx.Expect(err.Error()).ToEqual(`role reviewer-blocking: invalid value: mode ""`)
			})
		})
	})
}
