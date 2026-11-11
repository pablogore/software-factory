package wiring_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/slices/worker"
	"github.com/pablogore/software-factory/internal/wiring"
)

// The specs the simulated spec-writer hands in: one specgate rejects, for
// having no criteria or demo, and one it passes.
const (
	badSpec  = "STATUS: READY\n\n# S1: hacer algo\n\n## Valor\nQuien compra puede hacer algo.\n"
	goodSpec = "STATUS: READY\n\n# S1: hacer algo\n\n## Valor\nQuien compra puede hacer algo.\n\n" +
		"## Criterios de aceptación\n1. Se hace algo.\n\n## Demo\n1. Abrir la pantalla y verlo.\n"
)

var specWriter = domain.Role{Name: "spec-writer", Kind: domain.KindSpec, Tools: []string{"submit_result"}}

// specWorld is factoryd's store and bus with specgate subscribed, and the
// submitter a spec-writer's session hands its spec in through.
type specWorld struct {
	store  *memstore.Store
	events *bus.Bus
	sub    *worker.Submitter
	tokens *jobtoken.Tokens
}

func newSpecWorld(ctx *specs.Context) specWorld {
	bg := context.Background()
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(bg, domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "spec", Round: 1})).To(specs.BeNil())
	events := bus.New(store, fixedClock{})
	ctx.Expect(wiring.SubscribeSpecgate(events, wiring.Specgate(store, onePipeline{}, fixedClock{}))).To(specs.BeNil())
	ctx.Expect(events.Start(bg)).To(specs.BeNil())
	tokens := jobtoken.New()
	sub := &worker.Submitter{Tokens: tokens, Jobs: store, Pipelines: onePipeline{}, Locks: gatelock.New(), Clock: fixedClock{}}
	return specWorld{store: store, events: events, sub: sub, tokens: tokens}
}

// write runs one session of the spec-writer while the job is in spec: it
// gets the session's token, hands text in and lets the bus run specgate.
func (w specWorld) write(ctx *specs.Context, text string) {
	bg := context.Background()
	job := w.job(ctx)
	ctx.Expect(job.Stage).ToEqual("spec")
	hist, err := w.store.History(bg, "j-1")
	ctx.Expect(err).To(specs.BeNil())
	token, err := w.tokens.Issue(jobtoken.For(job, hist, specWriter))
	ctx.Expect(err).To(specs.BeNil())
	data, err := json.Marshal(domain.SpecResult{Title: "S1: hacer algo", Spec: text})
	ctx.Expect(err).To(specs.BeNil())
	_, err = w.sub.Submit(bg, token, "j-1", data)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(w.events.Dispatch(bg)).To(specs.BeNil())
}

func (w specWorld) job(ctx *specs.Context) domain.Job {
	job, _, err := w.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return job
}

// specgateSteps are the rejections and stage moves in the job's history, as
// a person reads it.
func (w specWorld) specgateSteps(ctx *specs.Context) []domain.Event {
	hist, err := w.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	var steps []domain.Event
	for _, r := range hist {
		switch r.Event.(type) {
		case domain.SpecRejected, domain.StageEntered, domain.NeedsHuman:
			steps = append(steps, r.Event)
		}
	}
	return steps
}

type specDemoRow struct {
	name   string
	writes []string
	stage  string
	steps  []domain.Event
}

func TestDemoSpecgateRetries(t *testing.T) {
	specs.Describe(t, "a spec-writer's specs through specgate in factoryd", func(s *specs.Spec) {
		at := fixedClock{}.Now()
		entered := func(stage string) domain.Event { return domain.StageEntered{Job: "j-1", Stage: stage, At: at} }
		rejected := func(n int) domain.Event { return domain.SpecRejected{Job: "j-1", Rejections: n, At: at} }
		rows := []specDemoRow{
			{"stops in needs-human on the third rejection, never built", []string{badSpec, badSpec, badSpec}, domain.StageNeedsHuman, []domain.Event{
				entered(domain.StageSpecgate), rejected(1), entered("spec"),
				entered(domain.StageSpecgate), rejected(2), entered("spec"),
				entered(domain.StageSpecgate), rejected(3),
				domain.NeedsHuman{Job: "j-1", Reason: "spec rejected by specgate 3 times", Cause: domain.CauseSpecEscalated, At: at},
			}},
			{"goes on to build when the second spec passes, with no one stepping in", []string{badSpec, goodSpec}, "build", []domain.Event{
				entered(domain.StageSpecgate), rejected(1), entered("spec"),
				entered(domain.StageSpecgate), entered("build"),
			}},
		}
		specs.Table(s, rows, func(r specDemoRow) string { return r.name }, func(ctx *specs.Context, r specDemoRow) {
			w := newSpecWorld(ctx)

			for _, text := range r.writes {
				w.write(ctx, text)
			}

			job := w.job(ctx)
			steps := w.specgateSteps(ctx)
			// Printed so `go test -v -run TestDemo` shows the job's way.
			var names []string
			for _, ev := range steps {
				names = append(names, ev.EventName())
			}
			ctx.Testing().Logf("%d specs handed in: job in %q after %d rejections; history %v", len(r.writes), job.Stage, job.SpecRejections, names)
			ctx.Expect(job.Stage).ToEqual(r.stage)
			ctx.Expect(steps).ToEqual(r.steps)
		})
	})
}
