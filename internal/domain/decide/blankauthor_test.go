package decide_test

import (
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// signedBy is a person's action on one route, signed by the name it is given.
type signedBy func(by string) ([]domain.Event, error)

type route struct {
	name string
	run  signedBy
}

type name struct{ name, by string }

type routeRow struct {
	name string
	run  signedBy
	by   string
	want error
}

func approval(by string) domain.HumanApproved {
	return domain.HumanApproved{Job: jobID, By: by, WaitSeq: 1, At: at(2)}
}

func onJob(job domain.Job, ev func(by string) domain.Event) signedBy {
	return func(by string) ([]domain.Event, error) { return decide.Decide(job, ev(by), defaultPipeline()) }
}

func onInitiative(in domain.Initiative, ev func(by string) domain.Event) signedBy {
	return func(by string) ([]domain.Event, error) { return decide.Initiative(in, ev(by)) }
}

func stopBy(by string) domain.Event     { return stop(by, 1) }
func approvalBy(by string) domain.Event { return approval(by) }

func messageBy(m domain.MessageReceived) func(by string) domain.Event {
	return func(by string) domain.Event { return signed(m, by) }
}

// humanRoutes are every action a person signs on a job or an initiative.
func humanRoutes() []route {
	review := newJob()
	review.Stage = "review"
	checkpoint := threeSlices(domain.CheckpointEverySlice)
	checkpoint.Stage = domain.InitiativeCheckpoint
	msg := messageBy(message(domain.AuthorPerson, true, 2))
	return []route{
		{"a stop", onJob(review, stopBy)},
		{"an OK in needs-human", onJob(needsHumanJob(), approvalBy)},
		{"an OK on a length exception", onJob(waitingJob(), approvalBy)},
		{"a needs-info answer", onJob(askingJob(), msg)},
		{"a [direction] drop", onJob(unmergedSlice("build"), messageBy(direction(domain.AuthorPerson, true, 2)))},
		{"a rework in needs-human", onJob(needsHumanJob(), msg)},
		{"a rework of a spec after specgate's escalation", onJob(specEscalated(), msg)},
		{"a rework of a spec on a length exception", onJob(waitingJob(), msg)},
		{"a reopen of a merged job", onJob(doneJob(), msg)},
		{"an initiative message", onInitiative(stuckAt(), messageBy(reply(domain.AuthorPerson, true, 2)))},
		{"an initiative OK", onInitiative(checkpoint, approvalBy)},
	}
}

// invisibleNames are names a reader cannot tell apart from nothing, one per
// class domain.Blank treats as blank.
var invisibleNames = []name{
	{"zero-width characters", "\u200b\u2060\ufeff"},
	{"control characters", "\x00\x1b\x7f"},
	{"a right-to-left mark", "\u200f"},
	{"a variation selector", "\ufe0f"},
	{"a Hangul filler", "\u3164"},
	{"a Braille blank", "\u2800"},
	{"an ideographic half fill space", "\u303f"},
	{"a null notehead", "\U0001d159"},
	{"a lone combining accent", "\u0301"},
	{"a lone enclosing mark", "\u20dd"},
	{"a private-use character", "\ue000"},
	{"a supplementary private-use character", "\U000f0000"},
	{"an unassigned code point", "\u0378"},
	{"a noncharacter", "\ufdd0"},
	{"invalid UTF-8", "\xff\xfe"},
	{"an encoded surrogate", "\xed\xa0\x80"},
	{"invisible characters around spaces", " \u200b \u2800 "},
}

var punctuationNames = []name{
	{"dots and dashes", "-.-"},
	{"an underscore", "_"},
	{"non-Latin punctuation", "\u3002\u00bf"},
	{"quotation marks", "\u00ab\u00bb\""},
	{"punctuation between invisible characters", "\u200b-\u200b"},
}

// realNames look odd but name someone, so no route refuses them.
var realNames = []name{
	{"a login", "pablogore"},
	{"a symbol", "+"},
	{"a dollar sign", "$"},
	{"an emoji", "\U0001f600"},
	{"an accented name", "Jos\u00e9"},
	{"a letter with a combining accent", "e\u0301"},
	{"a letter hidden among invisible characters", "\u200ba\u200b"},
}

func TestBlankAuthor(t *testing.T) {
	specs.Describe(t, "Decide on a person's action whose author names nobody", func(s *specs.Spec) {
		refused := func(names []name) []routeRow {
			var rows []routeRow
			for _, r := range humanRoutes() {
				for _, n := range names {
					rows = append(rows, routeRow{"refuses " + r.name + " signed with " + n.name, r.run, n.by, domain.ErrMissingField})
				}
			}
			return rows
		}
		refuse := func(ctx *specs.Context, r routeRow) {
			evs, err := r.run(r.by)

			ctx.Expect(err).To(specs.MatchError(r.want))
			ctx.Expect(evs).To(specs.BeEmpty())
		}

		s.When("the author is only invisible characters", func(s *specs.Spec) {
			specs.Table(s, refused(invisibleNames), func(r routeRow) string { return r.name }, refuse)
		})

		s.When("the author is only punctuation", func(s *specs.Spec) {
			specs.Table(s, refused(punctuationNames), func(r routeRow) string { return r.name }, refuse)
		})

		s.When("the author names someone", func(s *specs.Spec) {
			var rows []routeRow
			for _, r := range humanRoutes() {
				for _, n := range realNames {
					rows = append(rows, routeRow{"accepts " + r.name + " signed with " + n.name, r.run, n.by, nil})
				}
			}
			specs.Table(s, rows, func(r routeRow) string { return r.name }, func(ctx *specs.Context, r routeRow) {
				evs, err := r.run(r.by)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.Not(specs.BeEmpty()))
			})
		})

		s.When("an earlier check refuses the action first", func(s *specs.Spec) {
			other := func(ev func(by string) domain.Event) func(by string) domain.Event {
				return func(by string) domain.Event {
					switch e := ev(by).(type) {
					case domain.StopRequested:
						e.Job = "other"
						return e
					case domain.HumanApproved:
						e.Job = "other"
						return e
					case domain.MessageReceived:
						e.Job = "other"
						return e
					}
					return nil
				}
			}
			rejected := unmergedSlice(domain.StageRejected)
			msg := messageBy(message(domain.AuthorPerson, true, 2))
			drop := messageBy(direction(domain.AuthorPerson, true, 2))
			checkpoint := threeSlices(domain.CheckpointEverySlice)
			checkpoint.Stage = domain.InitiativeCheckpoint
			firsts := []struct {
				name string
				run  signedBy
				want error
			}{
				{"a stop for another job as another job's", onJob(needsHumanJob(), other(stopBy)), decide.ErrOtherJob},
				{"an OK for another job as another job's", onJob(needsHumanJob(), other(approvalBy)), decide.ErrOtherJob},
				{"a message for another job as another job's", onJob(needsHumanJob(), other(msg)), decide.ErrOtherJob},
				{"an initiative message for another job as another initiative's", onInitiative(stuckAt(), other(msg)), decide.ErrOtherInitiative},
				{"an initiative OK for another job as another initiative's", onInitiative(checkpoint, other(approvalBy)), decide.ErrOtherInitiative},
				{"a stop on a stopped job as stopped", onJob(stoppedJob(), stopBy), decide.ErrStopped},
				{"an OK on a stopped job as stopped", onJob(stoppedJob(), approvalBy), decide.ErrStopped},
				{"a message on a stopped job as stopped", onJob(stoppedJob(), msg), decide.ErrStopped},
				{"a [direction] on a stopped job as stopped", onJob(stoppedJob(), drop), decide.ErrStopped},
				{"a stop on a rejected job as illegal", onJob(rejected, stopBy), decide.ErrIllegalTransition},
				{"an OK on a rejected job as illegal", onJob(rejected, approvalBy), decide.ErrIllegalTransition},
				{"a message on a rejected job as illegal", onJob(rejected, msg), decide.ErrIllegalTransition},
				{"a [direction] on a rejected job as illegal", onJob(rejected, drop), decide.ErrIllegalTransition},
			}
			var rows []routeRow
			for _, f := range firsts {
				for _, n := range []name{invisibleNames[0], invisibleNames[5], punctuationNames[0]} {
					rows = append(rows, routeRow{"refuses " + f.name + " even signed with " + n.name, f.run, n.by, f.want})
				}
			}
			specs.Table(s, rows, func(r routeRow) string { return r.name }, func(ctx *specs.Context, r routeRow) {
				evs, err := r.run(r.by)

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(err).To(specs.Not(specs.MatchError(domain.ErrMissingField)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("the action is not from an allowed person", func(s *specs.Spec) {
			senders := []struct {
				name    string
				kind    domain.AuthorKind
				allowed bool
			}{
				{"a person not allowed", domain.AuthorPerson, false},
				{"a bot", domain.AuthorBot, true},
			}
			var rows []routeRow
			for _, sd := range senders {
				m := message(sd.kind, sd.allowed, 2)
				for _, n := range []name{invisibleNames[0], invisibleNames[5], punctuationNames[0]} {
					rows = append(rows,
						routeRow{"ignores a message from " + sd.name + " signed with " + n.name, onJob(needsHumanJob(), messageBy(m)), n.by, nil},
						routeRow{"ignores a message on a stopped job from " + sd.name + " signed with " + n.name, onJob(stoppedJob(), messageBy(m)), n.by, nil},
						routeRow{"ignores an initiative message from " + sd.name + " signed with " + n.name, onInitiative(stuckAt(), messageBy(m)), n.by, nil},
					)
				}
			}
			specs.Table(s, rows, func(r routeRow) string { return r.name }, func(ctx *specs.Context, r routeRow) {
				evs, err := r.run(r.by)

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("the action names another wait", func(s *specs.Spec) {
			stale := func(ev func(by string) domain.Event) signedBy {
				return onJob(secondWait(), ev)
			}
			rows := []routeRow{
				{"refuses an OK for another wait signed with zero-width characters for the name", stale(approvalBy), "\u200b", domain.ErrMissingField},
				{"refuses a message for another wait signed with a Braille blank for the name", stale(messageBy(message(domain.AuthorPerson, true, 2))), "\u2800", domain.ErrMissingField},
				{"refuses an OK for another wait signed with punctuation for the name", stale(approvalBy), "...", domain.ErrMissingField},
			}
			specs.Table(s, rows, func(r routeRow) string { return r.name }, func(ctx *specs.Context, r routeRow) {
				evs, err := r.run(r.by)

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(err).To(specs.Not(specs.MatchError(decide.ErrStaleWait)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})
	})
}
