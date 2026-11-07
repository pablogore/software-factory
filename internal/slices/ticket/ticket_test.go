package ticket_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/ticket"
)

var at = time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)

func pipeline() domain.Pipeline {
	return domain.Pipeline{
		Name: "default",
		Stages: []domain.Stage{
			{Name: "intake", Kind: domain.KindIntake, Roles: []string{"intake"}},
			{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		},
	}
}

// command is a /ticket from pablo, who is on telegram's allowlist.
func command() ticket.Trigger {
	return ticket.Trigger{
		Provider: "telegram", Thread: "chat-1/topic-7", Actor: "111", Kind: ticket.KindCommand,
		ID: "msg-42", Text: "agregar modo oscuro\n\nen la configuración", At: at,
	}
}

func allow() ticket.Allowlist {
	return ticket.Allowlist{"telegram": {"111"}, "github": {"pablogore"}}
}

// fixture is a handler over an empty in-memory store.
func fixture(ctx *specs.Context) (*ticket.Handler, *memstore.Store) {
	st, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	return &ticket.Handler{Jobs: st, Discards: st, Allow: allow(), Pipeline: pipeline()}, st
}

// replies are the confirmations st holds for job, decoded.
func replies(ctx *specs.Context, st *memstore.Store, job domain.JobID) []ticket.Reply {
	qs, err := st.Actions(context.Background(), job)
	ctx.Expect(err).To(specs.BeNil())
	rs := []ticket.Reply{}
	for _, q := range qs {
		ctx.Expect(q.Kind).ToEqual(ticket.ActionReply)
		var r ticket.Reply
		ctx.Expect(json.Unmarshal([]byte(q.Payload), &r)).To(specs.BeNil())
		rs = append(rs, r)
	}
	return rs
}

// nothingSaved checks st holds no job for t and no action at all.
func nothingSaved(ctx *specs.Context, st *memstore.Store, t ticket.Trigger) {
	_, _, err := st.Load(context.Background(), ticket.JobID(t))
	ctx.Expect(err).To(specs.MatchError(ports.ErrJobNotFound))
	due, err := st.DueActions(context.Background(), at.Add(time.Hour))
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(due).To(specs.BeEmpty())
}

func discards(ctx *specs.Context, st *memstore.Store) []ports.Discarded {
	ds, err := st.Discards(context.Background())
	ctx.Expect(err).To(specs.BeNil())
	return ds
}

func TestHandle(t *testing.T) {
	specs.Describe(t, "ticket.Handler", func(s *specs.Spec) {
		bg := context.Background()

		s.When("an allowed person asks for work", func(s *specs.Spec) {
			s.It("creates a job with its provider, thread and author, in the pipeline's first stage", func(ctx *specs.Context) {
				h, st := fixture(ctx)

				res, err := h.Handle(bg, command())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res).ToEqual(ticket.Result{Outcome: ticket.Created, Job: ticket.JobID(command())})
				job, _, err := st.Load(bg, res.Job)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job).ToEqual(domain.Job{
					ID: res.Job, Title: "agregar modo oscuro", Pipeline: "default", Stage: "intake", Round: 1,
					Origin:  domain.Origin{Provider: "telegram", Thread: "chat-1/topic-7", Author: "111"},
					Request: "agregar modo oscuro\n\nen la configuración",
				})
			})

			s.It("leaves one confirmation in the request's thread", func(ctx *specs.Context) {
				h, st := fixture(ctx)

				res, err := h.Handle(bg, command())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(replies(ctx, st, res.Job)).ToEqual([]ticket.Reply{
					{Provider: "telegram", Thread: "chat-1/topic-7", Text: "Job " + string(res.Job) + " created."},
				})
			})

			s.It("creates a job from an issue with the factory's label too", func(ctx *specs.Context) {
				h, st := fixture(ctx)
				issue := ticket.Trigger{Provider: "github", Thread: "pablogore/software-factory#39", Actor: "pablogore", Kind: ticket.KindIssue, ID: "39", Text: "Slice ticket", At: at}

				res, err := h.Handle(bg, issue)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res.Outcome).ToEqual(ticket.Created)
				job, _, err := st.Load(bg, res.Job)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Origin).ToEqual(domain.Origin{Provider: "github", Thread: "pablogore/software-factory#39", Author: "pablogore"})
				ctx.Expect(replies(ctx, st, res.Job)).ToEqual([]ticket.Reply{
					{Provider: "github", Thread: "pablogore/software-factory#39", Text: "Job " + string(res.Job) + " created."},
				})
			})

			s.It("gives different requests different jobs", func(ctx *specs.Context) {
				h, _ := fixture(ctx)
				other := command()
				other.ID = "msg-43"

				first, err := h.Handle(bg, command())
				ctx.Expect(err).To(specs.BeNil())
				second, err := h.Handle(bg, other)
				ctx.Expect(err).To(specs.BeNil())

				ctx.Expect(second.Outcome).ToEqual(ticket.Created)
				ctx.Expect(second.Job).To(specs.NotEqual(first.Job))
			})
		})

		s.When("the same request arrives again", func(s *specs.Spec) {
			s.It("creates no second job and no second confirmation", func(ctx *specs.Context) {
				h, st := fixture(ctx)
				first, err := h.Handle(bg, command())
				ctx.Expect(err).To(specs.BeNil())
				again := command()
				again.Text, again.At = "otra cosa", at.Add(time.Minute)

				res, err := h.Handle(bg, again)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res).ToEqual(ticket.Result{Outcome: ticket.Repeated, Job: first.Job})
				job, _, err := st.Load(bg, first.Job)
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Request).ToEqual(command().Text)
				ctx.Expect(replies(ctx, st, first.Job)).To(specs.HaveLen(1))
			})

			s.It("leaves one job and one confirmation when the copies arrive at once", func(ctx *specs.Context) {
				h, st := fixture(ctx)
				const n = 16
				results := make(chan ticket.Result, n)
				errs := make(chan error, n)
				for range n {
					go func() {
						res, err := h.Handle(bg, command())
						results <- res
						errs <- err
					}()
				}
				created := 0
				for range n {
					ctx.Expect(<-errs).To(specs.BeNil())
					res := <-results
					ctx.Expect(res.Job).ToEqual(ticket.JobID(command()))
					if res.Outcome == ticket.Created {
						created++
					} else {
						ctx.Expect(res.Outcome).ToEqual(ticket.Repeated)
					}
				}

				ctx.Expect(created).ToEqual(1)
				ctx.Expect(replies(ctx, st, ticket.JobID(command()))).To(specs.HaveLen(1))
			})
		})

		s.When("someone not on the allowlist asks for work", func(s *specs.Spec) {
			s.It("creates no job, replies nothing and keeps the request as discarded, without its text", func(ctx *specs.Context) {
				h, st := fixture(ctx)
				stranger := command()
				stranger.Actor = "666"

				res, err := h.Handle(bg, stranger)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res).ToEqual(ticket.Result{Outcome: ticket.Discarded})
				nothingSaved(ctx, st, stranger)
				ctx.Expect(discards(ctx, st)).ToEqual([]ports.Discarded{
					{Provider: "telegram", Thread: "chat-1/topic-7", Request: "msg-42", Author: "666", At: at},
				})
			})

			s.It("discards an id that is allowed only on another provider", func(ctx *specs.Context) {
				h, st := fixture(ctx)
				borrowed := command()
				borrowed.Actor = "pablogore" // allowed on github, not on telegram

				res, err := h.Handle(bg, borrowed)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res.Outcome).ToEqual(ticket.Discarded)
				nothingSaved(ctx, st, borrowed)
				ctx.Expect(discards(ctx, st)).To(specs.HaveLen(1))
			})

			s.It("discards a provider with no allowlist", func(ctx *specs.Context) {
				h, st := fixture(ctx)
				unknown := command()
				unknown.Provider = "discord"

				res, err := h.Handle(bg, unknown)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(res.Outcome).ToEqual(ticket.Discarded)
				nothingSaved(ctx, st, unknown)
			})

			s.It("keeps one discard when the same request arrives again", func(ctx *specs.Context) {
				h, st := fixture(ctx)
				stranger := command()
				stranger.Actor = "666"
				_, err := h.Handle(bg, stranger)
				ctx.Expect(err).To(specs.BeNil())

				_, err = h.Handle(bg, stranger)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(discards(ctx, st)).To(specs.HaveLen(1))
			})
		})

		rows := []refusedRow{
			{"without a provider", func(t *ticket.Trigger) { t.Provider = "" }, domain.ErrMissingField},
			{"without a thread", func(t *ticket.Trigger) { t.Thread = "" }, domain.ErrMissingField},
			{"without an author", func(t *ticket.Trigger) { t.Actor = "" }, domain.ErrMissingField},
			{"without an id", func(t *ticket.Trigger) { t.ID = "" }, domain.ErrMissingField},
			{"of free text, not a command", func(t *ticket.Trigger) { t.Kind = "message" }, ticket.ErrNotTicket},
			{"of a comment", func(t *ticket.Trigger) { t.Kind = "comment" }, ticket.ErrNotTicket},
		}
		specs.Table(s, rows, func(r refusedRow) string {
			return "refuses a trigger " + r.name + " and saves nothing"
		}, func(ctx *specs.Context, r refusedRow) {
			h, st := fixture(ctx)
			t := command()
			r.edit(&t)

			_, err := h.Handle(bg, t)

			ctx.Expect(err).To(specs.MatchError(r.sentinel))
			nothingSaved(ctx, st, t)
			ctx.Expect(discards(ctx, st)).To(specs.BeEmpty())
		})
	})
}

type refusedRow struct {
	name     string
	edit     func(*ticket.Trigger)
	sentinel error
}

type idRow struct {
	name string
	a, b ticket.Trigger
}

func TestJobID(t *testing.T) {
	specs.Describe(t, "ticket.JobID", func(s *specs.Spec) {
		s.It("is the same for the same provider, thread and id, whatever the text, author or time", func(ctx *specs.Context) {
			other := command()
			other.Text, other.Actor, other.At = "x", "222", at.Add(time.Hour)

			ctx.Expect(ticket.JobID(other)).ToEqual(ticket.JobID(command()))
		})

		mk := func(provider, thread, id string) ticket.Trigger {
			return ticket.Trigger{Provider: provider, Thread: thread, ID: id}
		}
		rows := []idRow{
			{"another provider", mk("telegram", "t", "1"), mk("discord", "t", "1")},
			{"another thread", mk("telegram", "t", "1"), mk("telegram", "u", "1")},
			{"another id", mk("telegram", "t", "1"), mk("telegram", "t", "2")},
			{"the same text split elsewhere", mk("telegram", "t1", "2"), mk("telegram", "t", "12")},
		}
		specs.Table(s, rows, func(r idRow) string {
			return "differs for " + r.name
		}, func(ctx *specs.Context, r idRow) {
			ctx.Expect(ticket.JobID(r.a)).To(specs.NotEqual(ticket.JobID(r.b)))
		})
	})
}
