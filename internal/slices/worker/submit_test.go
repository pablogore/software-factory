package worker_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

var at = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

type stopped struct{}

func (stopped) Now() time.Time { return at }

type pipelines map[string]domain.Pipeline

func (p pipelines) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if pl, ok := p[name]; ok {
		return pl, nil
	}
	return domain.Pipeline{}, fmt.Errorf("no pipeline %q", name)
}

func pipeline() domain.Pipeline {
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
	}}
}

// world is a submitter over an in-memory store with jobs j-1 and j-2 in
// build, and a builder's token for each.
type world struct {
	sub    *worker.Submitter
	store  *memstore.Store
	tokens *jobtoken.Tokens
	j1, j2 string
}

func newWorld(ctx *specs.Context) world {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	tokens := jobtoken.New()
	w := world{store: store, tokens: tokens}
	for _, id := range []domain.JobID{"j-1", "j-2"} {
		ctx.Expect(store.Create(context.Background(), domain.Job{ID: id, Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
	}
	w.j1 = w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1})
	w.j2 = w.issue(ctx, jobtoken.Grant{Job: "j-2", Role: "builder", Kind: domain.KindBuild, Round: 1})
	w.sub = &worker.Submitter{Tokens: tokens, Jobs: store, Pipelines: pipelines{"default": pipeline()}, Clock: stopped{}}
	return w
}

func (w world) issue(ctx *specs.Context, g jobtoken.Grant) string {
	token, err := w.tokens.Issue(g)
	ctx.Expect(err).To(specs.BeNil())
	return token
}

// apply decides ev on job id as it is saved now.
func (w world) apply(ctx *specs.Context, id domain.JobID, ev domain.Event) {
	_, v, err := w.store.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	_, _, err = w.store.Apply(context.Background(), id, v, pipeline(), ev)
	ctx.Expect(err).To(specs.BeNil())
}

// newRound takes job id from build, through a failed gate, to build in
// round 2, without its builder's token.
func (w world) newRound(ctx *specs.Context, id domain.JobID) {
	w.apply(ctx, id, domain.ResultSubmitted{Job: id, Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})
	w.apply(ctx, id, domain.GateFailed{Job: id, Run: domain.ChecksRun{Command: "make check", ExitCode: 2}, At: at})
	job, _, err := w.store.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(job.Stage).ToEqual("build")
	ctx.Expect(job.Round).ToEqual(2)
}

// untouched checks that job is still in build with no events saved.
func (w world) untouched(ctx *specs.Context, id domain.JobID) {
	job, _, err := w.store.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(job.Stage).ToEqual("build")
	hist, err := w.store.History(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(hist).To(specs.BeEmpty())
}

type invalidRow struct {
	name, data string
	want       error
	says       string
}

func invalidRows() []invalidRow {
	return []invalidRow{
		{"without its status", `{"summary":"done"}`, domain.ErrMissingField, "missing field: status"},
		{"with a status the kind has not", `{"status":"DONE"}`, domain.ErrInvalidValue, `status "DONE"`},
		{"with a field the kind has not", `{"status":"READY","verdict":"PASS"}`, domain.ErrInvalidValue, `unknown field "verdict"`},
		{"with a field of the wrong type", `{"status":1}`, domain.ErrInvalidValue, "status"},
		{"that is not JSON", `STATUS: READY`, domain.ErrInvalidValue, "not a JSON object"},
		{"with two JSON values", `{"status":"READY"} {"status":"BLOCKED"}`, domain.ErrInvalidValue, "more than one JSON value"},
		{"that is empty", ``, domain.ErrInvalidValue, "not a JSON object"},
	}
}

func TestSubmit(t *testing.T) {
	specs.Describe(t, "submitting a result with a job token", func(s *specs.Spec) {
		s.It("decides a valid result of the token's kind on its job, from its role", func(ctx *specs.Context) {
			w := newWorld(ctx)
			evs, err := w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"READY","summary":"done"}`))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.StageEntered{Job: "j-1", Stage: domain.StageGate, At: at}})

			job, _, err := w.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual(domain.StageGate)
			w.untouched(ctx, "j-2")
		})

		s.It("submits as the token's role, which the stage must list", func(ctx *specs.Context) {
			w := newWorld(ctx)
			other := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindBuild, Round: 1})
			_, err := w.sub.Submit(context.Background(), other, "j-1", []byte(`{"status":"READY"}`))
			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			w.untouched(ctx, "j-1")
		})

		s.When("the result is not valid for the kind", func(s *specs.Spec) {
			specs.Table(s, invalidRows(), func(r invalidRow) string { return "refuses one " + r.name + ", saying what is wrong" },
				func(ctx *specs.Context, r invalidRow) {
					w := newWorld(ctx)
					evs, err := w.sub.Submit(context.Background(), w.j1, "j-1", []byte(r.data))
					ctx.Expect(err).To(specs.MatchError(r.want))
					ctx.Expect(err.Error()).To(specs.Contain(r.says))
					ctx.Expect(err.Error()).To(specs.Contain("build result"))
					ctx.Expect(evs).To(specs.BeEmpty())
					w.untouched(ctx, "j-1")
				})
		})

		s.When("the token is not for this submission", func(s *specs.Spec) {
			s.It("refuses another job's token, saving nothing on either job", func(ctx *specs.Context) {
				w := newWorld(ctx)
				_, err := w.sub.Submit(context.Background(), w.j2, "j-1", []byte(`{"status":"READY"}`))
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				w.untouched(ctx, "j-1")
				w.untouched(ctx, "j-2")
			})

			s.It("refuses a submission without a token", func(ctx *specs.Context) {
				w := newWorld(ctx)
				_, err := w.sub.Submit(context.Background(), "", "j-1", []byte(`{"status":"READY"}`))
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrNoToken))
				w.untouched(ctx, "j-1")
			})

			s.It("refuses an unknown token", func(ctx *specs.Context) {
				w := newWorld(ctx)
				_, err := w.sub.Submit(context.Background(), "not-a-token", "j-1", []byte(`{"status":"READY"}`))
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
				w.untouched(ctx, "j-1")
			})

			s.It("checks the token before reading the result", func(ctx *specs.Context) {
				w := newWorld(ctx)
				_, err := w.sub.Submit(context.Background(), w.j2, "j-1", []byte(`not json`))
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				ctx.Expect(errors.Is(err, domain.ErrInvalidValue)).To(specs.BeFalse())
			})
		})

		s.It("revokes the token once its result is saved, so it submits no other", func(ctx *specs.Context) {
			w := newWorld(ctx)
			_, err := w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"READY"}`))
			ctx.Expect(err).To(specs.BeNil())
			before, err := w.store.History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			_, err = w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"BLOCKED","summary":"again"}`))
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
			after, err := w.store.History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(after).ToEqual(before)
		})

		s.It("keeps the token when its result is refused, so the agent can fix it", func(ctx *specs.Context) {
			w := newWorld(ctx)
			_, err := w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"DONE"}`))
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			_, err = w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"READY"}`))
			ctx.Expect(err).To(specs.BeNil())
		})

		s.It("keeps what a valid token's submissions came to until Take, and nothing for a token it refused", func(ctx *specs.Context) {
			w := newWorld(ctx)
			_, err := w.sub.Submit(context.Background(), w.j2, "j-1", []byte(`{"status":"READY"}`))
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(w.sub.Take(w.j2)).ToEqual(worker.Outcome{})
			_, err = w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"DONE"}`))
			ctx.Expect(w.sub.Take(w.j1)).ToEqual(worker.Outcome{Refused: err})
			ctx.Expect(w.sub.Take(w.j1)).ToEqual(worker.Outcome{})
			_, err = w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"READY"}`))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(w.sub.Take(w.j1)).ToEqual(worker.Outcome{Saved: true})
		})

		s.When("the job moved on since the token was issued", func(s *specs.Spec) {
			s.It("refuses a token of an earlier round, though the job is back in build, saving nothing", func(ctx *specs.Context) {
				w := newWorld(ctx)
				w.newRound(ctx, "j-1")
				before, err := w.store.History(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				evs, err := w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"READY"}`))
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
				ctx.Expect(errors.Is(err, jobtoken.ErrForbidden)).To(specs.BeFalse())
				ctx.Expect(err.Error()).To(specs.Contain("job token from an earlier round"))
				ctx.Expect(evs).To(specs.BeEmpty())
				after, err := w.store.History(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(after).ToEqual(before)
			})

			s.It("takes the result with a token issued in the new round", func(ctx *specs.Context) {
				w := newWorld(ctx)
				w.newRound(ctx, "j-1")
				job, _, err := w.store.Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				hist, err := w.store.History(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				fresh := w.issue(ctx, jobtoken.For(job, hist, "builder", domain.KindBuild))
				_, err = w.sub.Submit(context.Background(), fresh, "j-1", []byte(`{"status":"READY"}`))
				ctx.Expect(err).To(specs.BeNil())
			})

			s.It("refuses a token of an earlier entry into the same stage, in the same round", func(ctx *specs.Context) {
				w := newWorld(ctx)
				w.apply(ctx, "j-1", domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})
				w.apply(ctx, "j-1", domain.GatePassed{Job: "j-1", Run: domain.ChecksRun{Command: "make check"}, At: at})
				job, _, err := w.store.Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				hist, err := w.store.History(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				review := w.issue(ctx, jobtoken.For(job, hist, "reviewer-code", domain.KindReview))
				w.apply(ctx, "j-1", domain.BudgetExhausted{Job: "j-1", Spent: 9, At: at})
				w.apply(ctx, "j-1", domain.CapRaiseRequested{Job: "j-1", By: "pablogore", AuthorKind: domain.AuthorPerson, Allowed: true, Cap: domain.CapBudget, Value: 20, WaitSeq: 1, At: at})
				job, _, err = w.store.Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Stage).ToEqual("review")
				ctx.Expect(job.Round).ToEqual(1)
				_, err = w.sub.Submit(context.Background(), review, "j-1", []byte(`{"verdict":"PASS"}`))
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
			})
		})

		s.It("refuses a valid result the job's stage does not take, saving nothing", func(ctx *specs.Context) {
			w := newWorld(ctx)
			review := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Round: 1})
			_, err := w.sub.Submit(context.Background(), review, "j-1", []byte(`{"verdict":"PASS"}`))
			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			w.untouched(ctx, "j-1")
		})
	})
}
