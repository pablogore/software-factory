package worker_test

import (
	"context"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

func intakePipeline() domain.Pipeline {
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "intake", Kind: domain.KindIntake, Roles: []string{"intake"}},
		{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
	}}
}

// intakeWorld is a submitter over an in-memory store with job j-1 in intake
// and the intake's token for it.
type intakeWorld struct {
	sub   *worker.Submitter
	store *memstore.Store
	token string
}

func newIntakeWorld(ctx *specs.Context) intakeWorld {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "intake", Round: 1})).To(specs.BeNil())
	tokens := jobtoken.New()
	token, err := tokens.Issue(jobtoken.Grant{Job: "j-1", Role: "intake", Kind: domain.KindIntake, Round: 1})
	ctx.Expect(err).To(specs.BeNil())
	sub := &worker.Submitter{Tokens: tokens, Jobs: store, Pipelines: pipelines{"default": intakePipeline()}, Locks: gatelock.New(), Clock: stopped{}}
	return intakeWorld{sub: sub, store: store, token: token}
}

func (w intakeWorld) job(ctx *specs.Context) domain.Job {
	job, _, err := w.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return job
}

type incompleteRow struct {
	name, data, says string
	want             error
}

func TestDemoIntakeRiskSurfaces(t *testing.T) {
	specs.Describe(t, "an intake ACCEPT rates the job's risk and surfaces", func(s *specs.Spec) {
		s.It("saves the risk and surfaces of a complete ACCEPT on the job as intake gave them", func(ctx *specs.Context) {
			w := newIntakeWorld(ctx)
			evs, err := w.sub.Submit(context.Background(), w.token, "j-1", []byte(`{"decision":"ACCEPT","risk":"medium","surfaces":["ui","security"]}`))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{
				domain.IntakeAccepted{Job: "j-1", Risk: domain.RiskMedium, Surfaces: []string{"ui", "security"}, At: at},
				domain.StageEntered{Job: "j-1", Stage: "spec", At: at},
			})

			job := w.job(ctx)
			ctx.Expect(job.Stage).ToEqual("spec")
			ctx.Expect(job.Risk).ToEqual(domain.RiskMedium)
			ctx.Expect(job.Surfaces).ToEqual([]string{"ui", "security"})
		})

		s.It("takes an ACCEPT that says there are no surfaces", func(ctx *specs.Context) {
			w := newIntakeWorld(ctx)
			_, err := w.sub.Submit(context.Background(), w.token, "j-1", []byte(`{"decision":"ACCEPT","risk":"low","surfaces":[]}`))
			ctx.Expect(err).To(specs.BeNil())

			job := w.job(ctx)
			ctx.Expect(job.Stage).ToEqual("spec")
			ctx.Expect(job.Risk).ToEqual(domain.RiskLow)
			ctx.Expect(job.Surfaces).To(specs.BeEmpty())
		})

		s.When("the ACCEPT is incomplete", func(s *specs.Spec) {
			rows := []incompleteRow{
				{"without a risk", `{"decision":"ACCEPT","surfaces":[]}`, "missing field: risk", domain.ErrMissingField},
				{"with a risk that is not low, medium or high", `{"decision":"ACCEPT","risk":"critical","surfaces":[]}`, `invalid value: risk "critical"`, domain.ErrInvalidValue},
				{"saying nothing about surfaces", `{"decision":"ACCEPT","risk":"high"}`, "missing field: surfaces", domain.ErrMissingField},
				{"with surfaces null", `{"decision":"ACCEPT","risk":"high","surfaces":null}`, "missing field: surfaces", domain.ErrMissingField},
			}
			specs.Table(s, rows, func(r incompleteRow) string { return "refuses one " + r.name + ", and the agent sees why" },
				func(ctx *specs.Context, r incompleteRow) {
					w := newIntakeWorld(ctx)
					evs, err := w.sub.Submit(context.Background(), w.token, "j-1", []byte(r.data))
					ctx.Expect(err).To(specs.MatchError(r.want))
					ctx.Expect(err.Error()).To(specs.Contain("intake result: " + r.says))
					ctx.Expect(evs).To(specs.BeEmpty())
					ctx.Expect(w.sub.Take(w.token).Refused).To(specs.MatchError(r.want))

					job := w.job(ctx)
					ctx.Expect(job.Stage).ToEqual("intake")
					ctx.Expect(job.Risk).ToEqual(domain.Risk(""))
					hist, err := w.store.History(context.Background(), "j-1")
					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(hist).To(specs.BeEmpty())
				})
		})

		s.It("asks for more information without a risk or surfaces, as before", func(ctx *specs.Context) {
			w := newIntakeWorld(ctx)
			_, err := w.sub.Submit(context.Background(), w.token, "j-1", []byte(`{"decision":"NEEDS_INFO"}`))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(w.job(ctx).Stage).ToEqual(domain.StageNeedsInfo)
		})

		s.It("rejects without a risk or surfaces, as before", func(ctx *specs.Context) {
			w := newIntakeWorld(ctx)
			_, err := w.sub.Submit(context.Background(), w.token, "j-1", []byte(`{"decision":"REJECT","reason":"duplicate of #12"}`))
			ctx.Expect(err).To(specs.BeNil())
			job := w.job(ctx)
			ctx.Expect(job.Stage).ToEqual(domain.StageRejected)
			ctx.Expect(job.Reason).ToEqual("duplicate of #12")
		})
	})
}
