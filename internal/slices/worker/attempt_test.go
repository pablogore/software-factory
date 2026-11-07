package worker_test

import (
	"context"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

// scripted is an agent session whose turns each submit one result with its
// token, or none when the turn's entry is empty, then end. A turn past the
// script submits nothing; closed sessions refuse every prompt.
type scripted struct {
	sub     *worker.Submitter
	job     domain.JobID
	token   string
	turns   []string
	closed  bool
	prompts []string
	answers []error
}

func (s *scripted) Prompt(ctx context.Context, msg string) (<-chan domain.AgentProgressed, error) {
	if s.closed {
		return nil, ports.ErrSessionClosed
	}
	s.prompts = append(s.prompts, msg)
	i := len(s.prompts) - 1
	if i < len(s.turns) && s.turns[i] != "" {
		_, err := s.sub.Submit(ctx, s.token, s.job, []byte(s.turns[i]))
		s.answers = append(s.answers, err)
	}
	ch := make(chan domain.AgentProgressed, 1)
	ch <- domain.AgentProgressed{Job: s.job, Role: "builder", Kind: domain.ProgressEnded, StopReason: "end_turn"}
	close(ch)
	return ch, nil
}

func (w world) attempts() *worker.Attempts {
	return &worker.Attempts{Results: w.sub, Jobs: w.store, Pipelines: pipelines{"default": pipeline()}, Clock: stopped{}}
}

func (w world) session(turns ...string) *scripted {
	return &scripted{sub: w.sub, job: "j-1", token: w.j1, turns: turns}
}

// history lists the names of job id's saved events.
func (w world) history(ctx *specs.Context, id domain.JobID) []string {
	hist, err := w.store.History(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	names := []string{}
	for _, h := range hist {
		names = append(names, h.Event.EventName())
	}
	return names
}

// failed is job j-1's last saved event, which must be a failed attempt.
func (w world) failed(ctx *specs.Context) domain.AttemptFailed {
	hist, err := w.store.History(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(len(hist) > 0).To(specs.BeTrue())
	e, ok := hist[len(hist)-1].Event.(domain.AttemptFailed)
	ctx.Expect(ok).To(specs.BeTrue())
	return e
}

const (
	ready   = `{"status":"READY","summary":"done"}`
	invalid = `{"status":"DONE"}`
)

func TestAttempt(t *testing.T) {
	specs.Describe(t, "an agent's attempt at a job", func(s *specs.Spec) {
		s.It("asks once and moves on when the first turn hands in a valid result", func(ctx *specs.Context) {
			w := newWorld(ctx)
			sess := w.session(ready)
			ok, err := w.attempts().Run(context.Background(), sess, "j-1", "builder", w.j1, "build it")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(sess.prompts).ToEqual([]string{"build it"})
			ctx.Expect(w.history(ctx, "j-1")).ToEqual([]string{"StageEntered"})
		})

		s.When("the first turn ends without a saved result", func(s *specs.Spec) {
			s.It("asks again once, in the same session, saying no result was submitted", func(ctx *specs.Context) {
				w := newWorld(ctx)
				sess := w.session("", ready)
				ok, err := w.attempts().Run(context.Background(), sess, "j-1", "builder", w.j1, "build it")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(sess.prompts).ToEqual([]string{"build it", worker.Retry("no result submitted")})
			})

			s.It("asks again with the validation error of a refused result", func(ctx *specs.Context) {
				w := newWorld(ctx)
				sess := w.session(invalid, ready)
				_, err := w.attempts().Run(context.Background(), sess, "j-1", "builder", w.j1, "build it")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(sess.prompts).To(specs.HaveLen(2))
				ctx.Expect(sess.prompts[1]).To(specs.Contain(`your result was refused: worker: build result: invalid value: status "DONE"`))
			})

			s.It("records a valid result of the retry as if the first turn had handed it in, with the same token", func(ctx *specs.Context) {
				w := newWorld(ctx)
				sess := w.session(invalid, ready)
				ok, err := w.attempts().Run(context.Background(), sess, "j-1", "builder", w.j1, "build it")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(ok).To(specs.BeTrue())
				ctx.Expect(sess.answers).ToEqual([]error{sess.answers[0], nil})
				job, _, err := w.store.Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Stage).ToEqual(domain.StageGate)
				ctx.Expect(w.history(ctx, "j-1")).ToEqual([]string{"StageEntered"})
			})
		})

		s.When("the retry misses too", func(s *specs.Spec) {
			s.It("records a failed attempt with the reason, and asks no third time", func(ctx *specs.Context) {
				w := newWorld(ctx)
				sess := w.session(invalid, invalid, ready)
				ok, err := w.attempts().Run(context.Background(), sess, "j-1", "builder", w.j1, "build it")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(ok).To(specs.BeFalse())
				ctx.Expect(sess.prompts).To(specs.HaveLen(2))
				e := w.failed(ctx)
				ctx.Expect(e.Role).ToEqual("builder")
				ctx.Expect(e.At).ToEqual(at)
				ctx.Expect(e.Reason).To(specs.Contain(`your result was refused: worker: build result: invalid value: status "DONE"`))
				job, _, err := w.store.Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Stage).ToEqual("build")
			})

			s.It("records why when the agent never submits anything", func(ctx *specs.Context) {
				w := newWorld(ctx)
				sess := w.session()
				ok, err := w.attempts().Run(context.Background(), sess, "j-1", "builder", w.j1, "build it")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(ok).To(specs.BeFalse())
				ctx.Expect(sess.prompts).To(specs.HaveLen(2))
				ctx.Expect(w.history(ctx, "j-1")).ToEqual([]string{"AttemptFailed"})
				ctx.Expect(w.failed(ctx).Reason).ToEqual("no result submitted")
			})

			s.It("counts a session that ended as a turn without a result", func(ctx *specs.Context) {
				w := newWorld(ctx)
				sess := w.session()
				sess.closed = true
				ok, err := w.attempts().Run(context.Background(), sess, "j-1", "builder", w.j1, "build it")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(ok).To(specs.BeFalse())
				ctx.Expect(w.failed(ctx).Reason).To(specs.Contain("no result submitted, and the turn ended with: agent session closed"))
			})

			s.It("keeps the token after refused results, so the job can still take one", func(ctx *specs.Context) {
				w := newWorld(ctx)
				_, err := w.attempts().Run(context.Background(), w.session(invalid, invalid), "j-1", "builder", w.j1, "build it")
				ctx.Expect(err).To(specs.BeNil())
				_, err = w.sub.Submit(context.Background(), w.j1, "j-1", []byte(ready))
				ctx.Expect(err).To(specs.BeNil())
			})
		})

		s.It("asks for the result again in words that say why and how", func(ctx *specs.Context) {
			ctx.Snapshot("retry", worker.Retry(`your result was refused: worker: build result: invalid value: status "DONE"`))
		})
	})
}
