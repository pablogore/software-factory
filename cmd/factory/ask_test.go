package main

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/wiring"
)

type askingPipeline struct{}

func (askingPipeline) Pipeline(context.Context, string) (domain.Pipeline, error) {
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
	}}, nil
}

var askTools = []string{"submit_result", "progress", "ask_human"}

// asking is a worker API over an in-memory store with job j-1 in one stage
// of askingPipeline; issue gives a role a token for it.
type asking struct {
	factoryd
}

func startAsking(ctx *specs.Context, stage string) asking {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: stage, Round: 1})).To(specs.BeNil())
	tokens := jobtoken.New()
	srv := httptest.NewServer(wiring.WorkerAPI(tokens, store, askingPipeline{}, &gate{}, gatelock.New(), stopped{}, nil))
	ctx.Cleanup(srv.Close)
	return asking{factoryd{url: srv.URL, store: store, issuer: tokens}}
}

func (a asking) issue(ctx *specs.Context, role string, kind domain.Kind, tools []string) string {
	job, _, err := a.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	hist, err := a.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	token, err := a.issuer.Issue(jobtoken.For(job, hist, domain.Role{Name: role, Kind: kind, Tools: tools}))
	ctx.Expect(err).To(specs.BeNil())
	return token
}

func (a asking) job(ctx *specs.Context) domain.Job {
	job, _, err := a.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return job
}

// saved is the events j-1 saved, in order.
func (a asking) saved(ctx *specs.Context) []domain.Event {
	hist, err := a.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	evs := []domain.Event{}
	for _, r := range hist {
		evs = append(evs, r.Event)
	}
	return evs
}

const askArgs = `{"question":"Dark mode for everyone?","options":["yes","beta only"],"to":"product"}`

var askCLI = []string{"ask-human", "--to", "product", "--option", "yes", "--option", "beta only", "Dark mode for everyone?"}

func TestWorkerAskHuman(t *testing.T) {
	specs.Describe(t, "factory worker ask-human", func(s *specs.Spec) {
		s.It("leaves a spec-writer's job in needs-info with the question saved, as the MCP tool ask_human does", func(ctx *specs.Context) {
			byCLI := startAsking(ctx, "spec")
			r := cli(byCLI.env("j-1", byCLI.issue(ctx, "spec-writer", domain.KindSpec, askTools)), "", askCLI...)
			ctx.Expect(r.code).ToEqual(0)
			ctx.Expect(r.stdout).ToEqual("question recorded for job j-1: HumanAsked, NeedsInfo. The job waits for a person's answer " +
				"and your stage runs again with it; hand in no result and end your turn.\n")

			byMCP := startAsking(ctx, "spec")
			text, isErr, err := byMCP.call("ask_human", "j-1", byMCP.issue(ctx, "spec-writer", domain.KindSpec, askTools), askArgs)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(isErr).ToEqual(false)
			ctx.Expect(text).ToEqual(r.stdout)

			ctx.Expect(byCLI.saved(ctx)).ToEqual(byMCP.saved(ctx))
			ctx.Expect(byCLI.job(ctx)).ToEqual(byMCP.job(ctx))
			job := byCLI.job(ctx)
			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsInfo)
			ctx.Expect(job.Question).ToEqual("Dark mode for everyone?")
			saved := byCLI.saved(ctx)[0].(domain.HumanAsked)
			ctx.Expect(saved.Options).ToEqual([]string{"yes", "beta only"})
			ctx.Expect(saved.To).ToEqual(domain.AudienceProduct)
		})

		s.It("leaves an approver's job in needs-human with the question saved", func(ctx *specs.Context) {
			a := startAsking(ctx, "approve")
			r := cli(a.env("j-1", a.issue(ctx, "approver", domain.KindApprove, askTools)), "", askCLI...)
			ctx.Expect(r.code).ToEqual(0)
			job := a.job(ctx)
			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(job.Cause).ToEqual(domain.CauseAsked)
			ctx.Expect(job.Question).ToEqual("Dark mode for everyone?")
		})

		s.It("refuses a builder's token by the CLI and by the tool, and the job does not change", func(ctx *specs.Context) {
			a := startAsking(ctx, "build")
			token := a.issue(ctx, "builder", domain.KindBuild, []string{"submit_result", "progress", "run_gate"})
			before := a.job(ctx)

			r := cli(a.env("j-1", token), "", askCLI...)
			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stderr).ToEqual(`factory worker ask-human: refused (403): worker: ask human: job token not allowed: role "builder" may not use tool "ask_human"` + "\n")
			text, isErr, err := a.call("ask_human", "j-1", token, askArgs)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(isErr).ToEqual(true)
			ctx.Expect(text).ToEqual(`refused (403): worker: ask human: job token not allowed: role "builder" may not use tool "ask_human"`)

			ctx.Expect(a.saved(ctx)).To(specs.BeEmpty())
			ctx.Expect(a.job(ctx)).ToEqual(before)
		})

		s.It("refuses a question for no one, saying what is missing, and records nothing", func(ctx *specs.Context) {
			a := startAsking(ctx, "spec")
			r := cli(a.env("j-1", a.issue(ctx, "spec-writer", domain.KindSpec, askTools)), "", "ask-human", "Dark mode for everyone?")
			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stderr).To(specs.Contain("422"))
			ctx.Expect(r.stderr).To(specs.Contain("missing field: to"))
			ctx.Expect(a.saved(ctx)).To(specs.BeEmpty())
		})

		s.It("needs the question as its one argument", func(ctx *specs.Context) {
			a := startAsking(ctx, "spec")
			env := a.env("j-1", a.issue(ctx, "spec-writer", domain.KindSpec, askTools))
			ctx.Expect(cli(env, "", "ask-human", "--to", "product").code).ToEqual(2)
			ctx.Expect(cli(env, "", "ask-human", "--to", "product", "one", "two").code).ToEqual(2)
			ctx.Expect(a.saved(ctx)).To(specs.BeEmpty())
		})
	})
}
