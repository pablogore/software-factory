package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// signed is m with its author replaced by by.
func signed(m domain.MessageReceived, by string) domain.MessageReceived {
	m.Author = by
	return m
}

// needsHumanJob is a job waiting for a person after an approver escalation,
// its first wait.
func needsHumanJob() domain.Job {
	job := newJob()
	job.Stage, job.Round, job.Reason, job.Cause, job.WaitSeq = domain.StageNeedsHuman, 2, "approver escalated", domain.CauseEscalated, 1
	return job
}

type pathRow struct {
	name string
	run  func(domain.MessageReceived) ([]domain.Event, error)
	msg  domain.MessageReceived
}

type eventDecider struct {
	name string
	run  func(domain.Event) ([]domain.Event, error)
}

type authorRow struct {
	name string
	run  func(domain.MessageReceived) ([]domain.Event, error)
	msg  domain.MessageReceived
	want error
}

func TestAuthor(t *testing.T) {
	specs.Describe(t, "Decide on a person's message", func(s *specs.Spec) {
		onJob := func(job domain.Job) func(domain.MessageReceived) ([]domain.Event, error) {
			return func(m domain.MessageReceived) ([]domain.Event, error) {
				return decide.Decide(job, m, defaultPipeline())
			}
		}
		onInitiative := func(m domain.MessageReceived) ([]domain.Event, error) { return decide.Initiative(stuckAt(), m) }
		paths := []pathRow{
			{"rework in needs-human", onJob(needsHumanJob()), message(domain.AuthorPerson, true, 2)},
			{"rework of a spec after specgate's escalation", onJob(specEscalated()), message(domain.AuthorPerson, true, 2)},
			{"rework of a spec on a length exception", onJob(waitingJob()), message(domain.AuthorPerson, true, 2)},
			{"reopening a merged job", onJob(doneJob()), message(domain.AuthorPerson, true, 2)},
			{"a needs-info answer", onJob(askingJob()), message(domain.AuthorPerson, true, 2)},
			{"a [direction] drop", onJob(unmergedSlice("build")), direction(domain.AuthorPerson, true, 2)},
			{"an initiative answer", onInitiative, reply(domain.AuthorPerson, true, 2)},
		}
		blanks := []struct{ name, by string }{{"an empty name", ""}, {"a name of only spaces", " \t"}}

		s.When("it does not say who sent it", func(s *specs.Spec) {
			var rows []authorRow
			for _, p := range paths {
				for _, b := range blanks {
					rows = append(rows, authorRow{"refuses " + p.name + " with " + b.name, p.run, signed(p.msg, b.by), domain.ErrMissingField})
				}
			}
			specs.Table(s, rows, func(r authorRow) string { return r.name }, func(ctx *specs.Context, r authorRow) {
				evs, err := r.run(r.msg)

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("leaves a stuck initiative where it was", func(ctx *specs.Context) {
				in := stuckAt()

				evs, err := decide.Initiative(in, signed(reply(domain.AuthorPerson, true, 2), " "))

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(decide.ApplyInitiative(in, evs)).ToEqual(in)
			})
		})

		s.When("it says who sent it", func(s *specs.Spec) {
			specs.Table(s, paths, func(p pathRow) string { return "accepts " + p.name }, func(ctx *specs.Context, p pathRow) {
				evs, err := p.run(p.msg)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.Not(specs.BeEmpty()))
			})
		})

		s.When("the job no longer moves", func(s *specs.Spec) {
			rejected := unmergedSlice(domain.StageRejected)
			var rows []authorRow
			for _, b := range blanks {
				rows = append(rows,
					authorRow{"a stopped job refuses a message with " + b.name + " as it refuses any event", onJob(stoppedJob()),
						signed(message(domain.AuthorPerson, true, 2), b.by), decide.ErrStopped},
					authorRow{"a stopped job refuses a [direction] with " + b.name + " as it refuses any event", onJob(stoppedJob()),
						signed(direction(domain.AuthorPerson, true, 2), b.by), decide.ErrStopped},
					authorRow{"a rejected job refuses a message with " + b.name + " as it refuses any event", onJob(rejected),
						signed(message(domain.AuthorPerson, true, 2), b.by), decide.ErrIllegalTransition},
					authorRow{"a rejected job refuses a [direction] with " + b.name + " as it refuses any event", onJob(rejected),
						signed(direction(domain.AuthorPerson, true, 2), b.by), decide.ErrIllegalTransition},
				)
			}
			specs.Table(s, rows, func(r authorRow) string { return r.name }, func(ctx *specs.Context, r authorRow) {
				evs, err := r.run(r.msg)

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(err).To(specs.Not(specs.MatchError(domain.ErrMissingField)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("it is not from an allowed person", func(s *specs.Spec) {
			senders := []struct {
				name    string
				kind    domain.AuthorKind
				allowed bool
			}{
				{"a person not allowed", domain.AuthorPerson, false},
				{"a bot", domain.AuthorBot, true},
				{"CI", domain.AuthorCI, true},
			}
			var rows []authorRow
			for _, p := range paths {
				for _, sd := range senders {
					for _, b := range blanks {
						m := signed(p.msg, b.by)
						m.AuthorKind, m.Allowed = sd.kind, sd.allowed
						rows = append(rows, authorRow{"ignores " + p.name + " from " + sd.name + " with " + b.name, p.run, m, nil})
					}
				}
			}
			for _, b := range blanks {
				m := signed(message(domain.AuthorPerson, false, 2), b.by)
				rows = append(rows,
					authorRow{"ignores a message on a stopped job from a person not allowed with " + b.name, onJob(stoppedJob()), m, nil},
					authorRow{"ignores a message on a rejected job from a person not allowed with " + b.name, onJob(unmergedSlice(domain.StageRejected)), m, nil},
				)
			}
			specs.Table(s, rows, func(r authorRow) string { return r.name }, func(ctx *specs.Context, r authorRow) {
				evs, err := r.run(r.msg)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("the message is missing", func(s *specs.Spec) {
			rows := []eventDecider{
				{"refuses a nil message to a job before asking who sent it", func(ev domain.Event) ([]domain.Event, error) {
					return decide.Decide(needsHumanJob(), ev, defaultPipeline())
				}},
				{"refuses a nil message to an initiative before asking who sent it", func(ev domain.Event) ([]domain.Event, error) {
					return decide.Initiative(stuckAt(), ev)
				}},
			}
			specs.Table(s, rows, func(r eventDecider) string { return r.name }, func(ctx *specs.Context, r eventDecider) {
				evs, err := r.run((*domain.MessageReceived)(nil))

				ctx.Expect(err).To(specs.MatchError(decide.ErrIllegalTransition))
				ctx.Expect(err).To(specs.Not(specs.MatchError(domain.ErrMissingField)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})
	})
}
