package spec_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/slices/spec"
)

type onePipeline struct{}

func (onePipeline) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if name != "default" {
		return domain.Pipeline{}, fmt.Errorf("no pipeline %q", name)
	}
	return pipeline(), nil
}

type stopped struct{}

func (stopped) Now() time.Time { return at }

const goodSpec = "STATUS: READY\n\n# S1: hacer algo\n\n## Valor\nQuien compra puede hacer algo.\n\n" +
	"## Criterios de aceptación\n1. Se hace algo.\n\n## Demo\n1. Abrir la pantalla y verlo.\n"

// storeAt returns a store with job j-1 in stage and specgate over it.
func storeAt(ctx *specs.Context, stage string) (*memstore.Store, *spec.Specgate) {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: stage, Round: 1})).To(specs.BeNil())
	return store, &spec.Specgate{Jobs: store, History: store, Pipelines: onePipeline{}, Clock: stopped{}}
}

// written is a store with job j-1 at specgate, its spec role having handed
// in texts, in order, each but the last rejected.
func written(ctx *specs.Context, texts ...string) (*memstore.Store, *spec.Specgate) {
	store, g := storeAt(ctx, "spec")
	apply := func(ev domain.Event) {
		_, v, err := store.Load(context.Background(), "j-1")
		ctx.Expect(err).To(specs.BeNil())
		_, _, err = store.Apply(context.Background(), "j-1", v, pipeline(), ev)
		ctx.Expect(err).To(specs.BeNil())
	}
	for i, text := range texts {
		if i > 0 {
			apply(domain.SpecgateFailed{Job: "j-1", At: at})
		}
		apply(domain.ResultSubmitted{Job: "j-1", Role: "spec-writer", Result: domain.SpecResult{Title: "S1", Spec: text}, At: at})
	}
	return store, g
}

func stageOf(ctx *specs.Context, store *memstore.Store) domain.Job {
	job, _, err := store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return job
}

func TestSpecgateRun(t *testing.T) {
	specs.Describe(t, "specgate run on a job", func(s *specs.Spec) {
		bg := context.Background()

		s.It("moves a job whose spec passes on to build", func(ctx *specs.Context) {
			store, g := written(ctx, goodSpec)

			evs, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.StageEntered{Job: "j-1", Stage: "build", At: at}})
			ctx.Expect(stageOf(ctx, store).Stage).ToEqual("build")
		})

		s.It("sends a job whose spec fails back to its writer, counting the rejection", func(ctx *specs.Context) {
			store, g := written(ctx, "STATUS: READY\n\n# S1\n")

			evs, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{
				domain.SpecRejected{Job: "j-1", Rejections: 1, At: at},
				domain.StageEntered{Job: "j-1", Stage: "spec", At: at},
			})
			ctx.Expect(stageOf(ctx, store).SpecRejections).ToEqual(1)
		})

		s.It("checks the spec handed in last, not an earlier one", func(ctx *specs.Context) {
			store, g := written(ctx, "STATUS: READY\n\n# S1\n", goodSpec)

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(stageOf(ctx, store).Stage).ToEqual("build")
		})

		s.It("refuses a job that is not at specgate and changes nothing", func(ctx *specs.Context) {
			store, g := storeAt(ctx, "build")

			evs, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(spec.ErrNotAtSpecgate))
			ctx.Expect(evs).To(specs.BeEmpty())
			ctx.Expect(stageOf(ctx, store).Stage).ToEqual("build")
		})

		s.It("refuses a job at specgate with no spec in its history", func(ctx *specs.Context) {
			store, g := storeAt(ctx, domain.StageSpecgate)

			_, err := g.Run(bg, "j-1")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(stageOf(ctx, store).Stage).ToEqual(domain.StageSpecgate)
		})

		s.When("handling bus events", func(s *specs.Spec) {
			s.It("checks a job that entered specgate", func(ctx *specs.Context) {
				store, g := written(ctx, goodSpec)

				ctx.Expect(g.Handle(bg, domain.StageEntered{Job: "j-1", Stage: domain.StageSpecgate, At: at})).To(specs.BeNil())

				ctx.Expect(stageOf(ctx, store).Stage).ToEqual("build")
			})

			s.It("is done with an entry the job already left, as when the bus hands it over again", func(ctx *specs.Context) {
				store, g := written(ctx, goodSpec)
				entered := domain.StageEntered{Job: "j-1", Stage: domain.StageSpecgate, At: at}
				ctx.Expect(g.Handle(bg, entered)).To(specs.BeNil())

				ctx.Expect(g.Handle(bg, entered)).To(specs.BeNil())

				ctx.Expect(stageOf(ctx, store).Stage).ToEqual("build")
			})

			s.It("ignores entries into other stages", func(ctx *specs.Context) {
				store, g := written(ctx, goodSpec)

				ctx.Expect(g.Handle(bg, domain.StageEntered{Job: "j-1", Stage: "build", At: at})).To(specs.BeNil())

				ctx.Expect(stageOf(ctx, store).Stage).ToEqual(domain.StageSpecgate)
			})
		})
	})
}
