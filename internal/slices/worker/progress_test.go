package worker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

func message(role, text string) domain.AgentProgressed {
	return domain.AgentProgressed{Job: "j-1", Role: role, Kind: domain.ProgressMessage, Text: text, At: at}
}

// saved is job id's history as events.
func (w world) saved(ctx *specs.Context, id domain.JobID) []domain.Event {
	hist, err := w.store.History(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	evs := make([]domain.Event, len(hist))
	for i, r := range hist {
		evs[i] = r.Event
	}
	return evs
}

type badProgressRow struct {
	name, text string
	want       error
}

func TestProgress(t *testing.T) {
	specs.Describe(t, "reporting progress with a job token", func(s *specs.Spec) {
		s.It("records each message as an event of its job, from the token's role, in the order they came", func(ctx *specs.Context) {
			w := newWorld(ctx)
			evs, err := w.sub.Progress(context.Background(), w.j1, "j-1", "reading the spec")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{message("builder", "reading the spec")})
			_, err = w.sub.Progress(context.Background(), w.j1, "j-1", "running the tests")
			ctx.Expect(err).To(specs.BeNil())

			ctx.Expect(w.saved(ctx, "j-1")).ToEqual([]domain.Event{message("builder", "reading the spec"), message("builder", "running the tests")})
			job, _, err := w.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual("build")
			w.untouched(ctx, "j-2")
		})

		s.It("takes progress from any role, even one whose result the stage does not take", func(ctx *specs.Context) {
			w := newWorld(ctx)
			review := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Round: 1})
			_, err := w.sub.Progress(context.Background(), review, "j-1", "reading the diff")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(w.saved(ctx, "j-1")).ToEqual([]domain.Event{message("reviewer-code", "reading the diff")})
		})

		s.It("keeps the token, so its result is still submitted after the messages", func(ctx *specs.Context) {
			w := newWorld(ctx)
			_, err := w.sub.Progress(context.Background(), w.j1, "j-1", "almost done")
			ctx.Expect(err).To(specs.BeNil())
			evs, err := w.sub.Submit(context.Background(), w.j1, "j-1", []byte(`{"status":"READY"}`))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(evs).ToEqual([]domain.Event{domain.StageEntered{Job: "j-1", Stage: domain.StageGate, At: at}})
			ctx.Expect(w.sub.Take(w.j1).Saved).To(specs.BeTrue())
		})

		rows := []badProgressRow{
			{"an empty message", "", domain.ErrMissingField},
			{"a blank message", "  \n", domain.ErrMissingField},
			{"a message over the longest length", strings.Repeat("x", domain.MaxProgressText+1), domain.ErrInvalidValue},
		}
		specs.Table(s, rows, func(r badProgressRow) string { return "refuses " + r.name + ", saving nothing" }, func(ctx *specs.Context, r badProgressRow) {
			w := newWorld(ctx)
			evs, err := w.sub.Progress(context.Background(), w.j1, "j-1", r.text)
			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(evs).To(specs.BeEmpty())
			w.untouched(ctx, "j-1")
		})

		s.It("refuses another job's token, saving nothing on either job", func(ctx *specs.Context) {
			w := newWorld(ctx)
			_, err := w.sub.Progress(context.Background(), w.j2, "j-1", "hello")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			w.untouched(ctx, "j-1")
			w.untouched(ctx, "j-2")
		})

		s.It("refuses a token of an earlier round, saving nothing", func(ctx *specs.Context) {
			w := newWorld(ctx)
			w.newRound(ctx, "j-1")
			before := w.saved(ctx, "j-1")
			_, err := w.sub.Progress(context.Background(), w.j1, "j-1", "still here")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
			ctx.Expect(w.saved(ctx, "j-1")).ToEqual(before)
		})

		s.It("records nothing as a submission outcome", func(ctx *specs.Context) {
			w := newWorld(ctx)
			_, err := w.sub.Progress(context.Background(), w.j1, "j-1", "")
			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(w.sub.Take(w.j1).Refused).To(specs.BeNil())
		})

		s.It("refuses a role whose tools do not list progress, naming the tool and the role and saving nothing", func(ctx *specs.Context) {
			w := newWorld(ctx)
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "only-submits", Kind: domain.KindBuild, Tools: []string{"submit_result"}, Round: 1})
			evs, err := w.sub.Progress(context.Background(), token, "j-1", "sneaking in")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(err.Error()).To(specs.Contain(`role "only-submits" may not use tool "progress"`))
			ctx.Expect(evs).To(specs.BeEmpty())
			w.untouched(ctx, "j-1")
			ctx.Expect(w.sub.Take(token)).ToEqual(worker.Outcome{})
		})
	})
}
