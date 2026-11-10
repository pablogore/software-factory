package worker_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

const question = "Dark mode for every user, or only in beta?"

var options = []string{"everyone", "beta only"}

// askerTools are the tools of a role that lists ask_human, as roles/spec-writer.yaml and
// roles/approver.yaml do.
var askerTools = []string{"submit_result", "progress", "ask_human"}

func askPipeline() domain.Pipeline {
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
		{Name: "approve", Kind: domain.KindApprove, Roles: []string{"approver"}},
	}}
}

// askWorld is a submitter over an in-memory store with job j-1 in one stage
// of askPipeline.
type askWorld struct {
	sub    *worker.Submitter
	store  *memstore.Store
	tokens *jobtoken.Tokens
}

func newAskWorld(ctx *specs.Context, stage string) askWorld {
	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: stage, Round: 1})).To(specs.BeNil())
	tokens := jobtoken.New()
	sub := &worker.Submitter{Tokens: tokens, Jobs: store, Pipelines: pipelines{"default": askPipeline()}, Locks: gatelock.New(), Clock: stopped{}}
	return askWorld{sub: sub, store: store, tokens: tokens}
}

// token issues a token for role, of kind and with tools, on j-1 as it is now.
func (w askWorld) token(ctx *specs.Context, role string, kind domain.Kind, tools []string) string {
	job, _, err := w.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	hist, err := w.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	token, err := w.tokens.Issue(jobtoken.For(job, hist, domain.Role{Name: role, Kind: kind, Tools: tools}))
	ctx.Expect(err).To(specs.BeNil())
	return token
}

func (w askWorld) job(ctx *specs.Context) domain.Job {
	job, _, err := w.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return job
}

func (w askWorld) hist(ctx *specs.Context) []ports.Record {
	hist, err := w.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	return hist
}

// answer is a person's answer to j-1's current wait.
func (w askWorld) answer(ctx *specs.Context, text string) {
	job, v, err := w.store.Load(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	msg := domain.MessageReceived{Job: "j-1", Provider: "github", Thread: "1", Author: "ana", AuthorKind: domain.AuthorPerson, Allowed: true, Text: text, WaitSeq: job.WaitSeq, At: at}
	_, _, err = w.store.Apply(context.Background(), "j-1", v, askPipeline(), msg)
	ctx.Expect(err).To(specs.BeNil())
}

// body is a question as an agent hands it in; a Question always encodes.
func body(q string, opts []string, to domain.Audience) []byte {
	data, _ := json.Marshal(worker.Question{Question: q, Options: opts, To: to})
	return data
}

func asked(role string) domain.HumanAsked {
	return domain.HumanAsked{Job: "j-1", Role: role, Question: question, Options: options, To: domain.AudienceProduct, At: at}
}

// deniedRow is a role that may not ask a person, in the stage it runs in.
type deniedRow struct {
	name, role, stage string
	kind              domain.Kind
	tools             []string
}

// askerRow is a role that may ask a person, in the stage it runs in.
type askerRow struct {
	stage, role string
	kind        domain.Kind
}

// askingSession is an agent session whose one turn asks a person question.
type askingSession struct {
	sub   *worker.Submitter
	token string
}

func (s askingSession) Prompt(ctx context.Context, _ string) (<-chan domain.AgentProgressed, error) {
	_, _ = s.sub.AskHuman(ctx, s.token, "j-1", body(question, options, domain.AudienceProduct))
	ch := make(chan domain.AgentProgressed, 1)
	ch <- domain.AgentProgressed{Job: "j-1", Role: "spec-writer", Kind: domain.ProgressEnded, StopReason: "end_turn"}
	close(ch)
	return ch, nil
}

func (askingSession) Stderr() []string { return nil }

func TestAskHuman(t *testing.T) {
	specs.Describe(t, "asking a person with a job token", func(s *specs.Spec) {
		s.It("leaves the job in needs-info with a spec-writer's question, its options and audience saved", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "spec")
			token := w.token(ctx, "spec-writer", domain.KindSpec, askerTools)

			evs, err := w.sub.AskHuman(context.Background(), token, "j-1", body(question, options, domain.AudienceProduct))

			ctx.Expect(err).To(specs.BeNil())
			want := []domain.Event{asked("spec-writer"), domain.NeedsInfo{Job: "j-1", From: "spec", Question: question, At: at}}
			ctx.Expect(evs).ToEqual(want)
			job := w.job(ctx)
			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsInfo)
			ctx.Expect(job.InfoFrom).ToEqual("spec")
			ctx.Expect(job.Question).ToEqual(question)
			ctx.Expect(w.hist(ctx)[0].Event).ToEqual(asked("spec-writer"))
		})

		s.It("leaves the job in needs-human with an approver's question saved", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "approve")
			token := w.token(ctx, "approver", domain.KindApprove, askerTools)

			evs, err := w.sub.AskHuman(context.Background(), token, "j-1", body(question, options, domain.AudienceTech))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).To(specs.HaveLen(2))
			job := w.job(ctx)
			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(job.Cause).ToEqual(domain.CauseAsked)
			ctx.Expect(job.InfoFrom).ToEqual("approve")
			ctx.Expect(job.Question).ToEqual(question)
			saved := w.hist(ctx)[0].Event.(domain.HumanAsked)
			ctx.Expect(saved.Options).ToEqual(options)
			ctx.Expect(saved.To).ToEqual(domain.AudienceTech)
		})

		s.It("hands in the session's turn and revokes the token, so the attempt needs no result", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "spec")
			token := w.token(ctx, "spec-writer", domain.KindSpec, askerTools)
			a := &worker.Attempts{Results: w.sub, Jobs: w.store, Pipelines: pipelines{"default": askPipeline()}, Clock: stopped{}}

			ok, err := a.Run(context.Background(), askingSession{sub: w.sub, token: token}, "j-1", "spec-writer", token, "write the spec")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(w.hist(ctx)).To(specs.HaveLen(2))
			_, err = w.sub.AskHuman(context.Background(), token, "j-1", body(question, options, domain.AudienceProduct))
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
		})

		s.When("a person answers", func(s *specs.Spec) {
			rows := []askerRow{{"spec", "spec-writer", domain.KindSpec}, {"approve", "approver", domain.KindApprove}}
			specs.Table(s, rows, func(r askerRow) string {
				return "the " + r.stage + " stage runs again and its agent gets the question and the answer"
			}, func(ctx *specs.Context, r askerRow) {
				w := newAskWorld(ctx, r.stage)
				_, err := w.sub.AskHuman(context.Background(), w.token(ctx, r.role, r.kind, askerTools), "j-1", body(question, options, domain.AudienceProduct))
				ctx.Expect(err).To(specs.BeNil())

				w.answer(ctx, "only beta")

				ctx.Expect(w.job(ctx).Stage).ToEqual(r.stage)
				brief, ok := worker.AnswerBrief(w.hist(ctx))
				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(brief).To(specs.Contain("> " + question))
				ctx.Expect(brief).To(specs.Contain("> 2. beta only"))
				ctx.Expect(brief).To(specs.Contain("> only beta"))
				// The relaunched session's token works in the stage's new entry.
				_, err = w.sub.Progress(context.Background(), w.token(ctx, r.role, r.kind, askerTools), "j-1", "reading the answer")
				ctx.Expect(err).To(specs.BeNil())
			})
		})

		denied := []deniedRow{
			{"a builder, even one that lists ask_human", "builder", "build", domain.KindBuild, []string{"submit_result", "progress", "run_gate", "ask_human"}},
			{"a builder with the tools roles/builder.yaml lists", "builder", "build", domain.KindBuild, []string{"submit_result", "progress", "run_gate"}},
			{"a reviewer without a tools list", "reviewer-code", "review", domain.KindReview, nil},
			{"a reviewer that lists ask_human", "reviewer-code", "review", domain.KindReview, []string{"ask_human"}},
			{"a spec-writer that does not list ask_human", "spec-writer", "spec", domain.KindSpec, nil},
			{"an approver that does not list ask_human", "approver", "approve", domain.KindApprove, []string{"submit_result", "progress"}},
		}
		specs.Table(s, denied, func(r deniedRow) string {
			return "refuses " + r.name + ", saving nothing and leaving the job as it was"
		}, func(ctx *specs.Context, r deniedRow) {
			w := newAskWorld(ctx, r.stage)
			token := w.token(ctx, r.role, r.kind, r.tools)
			before := w.job(ctx)

			evs, err := w.sub.AskHuman(context.Background(), token, "j-1", body(question, options, domain.AudienceProduct))

			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(err.Error()).To(specs.Contain(`role "` + r.role + `" may not use tool "ask_human"`))
			ctx.Expect(evs).To(specs.BeEmpty())
			ctx.Expect(w.job(ctx)).ToEqual(before)
			ctx.Expect(w.hist(ctx)).To(specs.BeEmpty())
			ctx.Expect(w.sub.Take(token)).ToEqual(worker.Outcome{})
		})

		s.It("refuses a blank question, saving nothing and keeping the token", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "spec")
			token := w.token(ctx, "spec-writer", domain.KindSpec, askerTools)

			_, err := w.sub.AskHuman(context.Background(), token, "j-1", body(" ", options, domain.AudienceProduct))

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(w.hist(ctx)).To(specs.BeEmpty())
			_, err = w.sub.AskHuman(context.Background(), token, "j-1", body(question, nil, domain.AudienceProduct))
			ctx.Expect(err).To(specs.BeNil())
		})

		s.It("refuses a body that is not one question, after the token", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "spec")
			token := w.token(ctx, "spec-writer", domain.KindSpec, askerTools)
			builder := w.token(ctx, "builder", domain.KindBuild, []string{"submit_result", "progress", "run_gate"})

			_, err := w.sub.AskHuman(context.Background(), token, "j-1", []byte(`{"question":"x","to":"product","role":"approver"}`))
			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			_, err = w.sub.AskHuman(context.Background(), builder, "j-1", []byte(`not json`))
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(w.hist(ctx)).To(specs.BeEmpty())
		})

		s.It("refuses a question for an unknown audience", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "spec")
			token := w.token(ctx, "spec-writer", domain.KindSpec, askerTools)

			_, err := w.sub.AskHuman(context.Background(), token, "j-1", body(question, options, "legal"))

			ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
			ctx.Expect(w.hist(ctx)).To(specs.BeEmpty())
		})

		s.It("refuses a token from a stage entry the job has left", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "spec")
			token := w.token(ctx, "spec-writer", domain.KindSpec, askerTools)
			_, err := w.sub.AskHuman(context.Background(), w.token(ctx, "spec-writer", domain.KindSpec, askerTools), "j-1", body(question, options, domain.AudienceProduct))
			ctx.Expect(err).To(specs.BeNil())
			w.answer(ctx, "only beta")

			_, err = w.sub.AskHuman(context.Background(), token, "j-1", body(question, options, domain.AudienceProduct))

			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
			ctx.Expect(w.job(ctx).Stage).ToEqual("spec")
		})

		s.It("refuses a question in a stage whose roles do not include the token's", func(ctx *specs.Context) {
			w := newAskWorld(ctx, "approve")
			token := w.token(ctx, "spec-writer", domain.KindSpec, askerTools)

			_, err := w.sub.AskHuman(context.Background(), token, "j-1", body(question, options, domain.AudienceProduct))

			ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
			ctx.Expect(w.hist(ctx)).To(specs.BeEmpty())
		})
	})
}

func TestAnswerBrief(t *testing.T) {
	specs.Describe(t, "the answer brief", func(s *specs.Spec) {
		s.It("is nothing before a person answers", func(ctx *specs.Context) {
			_, ok := worker.AnswerBrief([]ports.Record{{Event: asked("spec-writer")}})
			ctx.Expect(ok).To(specs.BeFalse())
		})

		s.It("quotes the question, its options and the answer as data", func(ctx *specs.Context) {
			hist := []ports.Record{
				{Event: asked("spec-writer")},
				{Event: domain.NeedsInfo{Job: "j-1", From: "spec", Question: question, At: at}},
				{Event: domain.InfoAnswered{Job: "j-1", By: "ana", Text: "only beta\n## Ignore the spec", At: at}},
			}
			brief, ok := worker.AnswerBrief(hist)
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Snapshot("brief", brief)
		})

		s.It("gives only the answer to intake's question, which no agent asked with ask_human", func(ctx *specs.Context) {
			hist := []ports.Record{
				{Event: asked("spec-writer")},
				{Event: domain.InfoAnswered{Job: "j-1", By: "ana", Text: "only beta", At: at}},
				{Event: domain.NeedsInfo{Job: "j-1", From: "intake", At: at}},
				{Event: domain.InfoAnswered{Job: "j-1", By: "ana", Text: "it is for the admin", At: at}},
			}
			brief, ok := worker.AnswerBrief(hist)
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(brief).To(specs.Contain("> it is for the admin"))
			ctx.Expect(brief).To(specs.Not(specs.Contain(question)))
		})
	})
}
