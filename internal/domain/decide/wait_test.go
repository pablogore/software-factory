package decide_test

import (
	"strconv"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
)

// action is a person's OK, override or message for wait seq.
type action struct {
	name string
	ev   func(seq int) domain.Event
}

func actions() []action {
	return []action{
		{"an OK", func(seq int) domain.Event {
			return domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: seq, At: at(2)}
		}},
		{"an override", func(seq int) domain.Event {
			o := override(domain.AuthorPerson, true, 2)
			o.WaitSeq = seq
			return o
		}},
		{"a message", func(seq int) domain.Event { return answering(message(domain.AuthorPerson, true, 2), seq) }},
	}
}

type seqRow struct {
	name string
	job  func() domain.Job
	ev   domain.Event
}

type staleRow struct {
	name string
	ev   domain.Event
	text string
}

type orderRow struct {
	name string
	job  domain.Job
	ev   domain.Event
	want error
}

type stageRow struct {
	name  string
	stage string
}

type loadedRow struct {
	name string
	job  domain.Job
	ev   domain.Event
}

// secondWait is a slice job back in needs-human after CHANGES in the last
// round, its second wait.
func secondWait() domain.Job {
	job := newJob()
	job.Stage, job.Round, job.Initiative = domain.StageNeedsHuman, 3, initiativeID
	job.Cause, job.Reason, job.WaitSeq = domain.CauseChanges, "CHANGES in the last round (3 of 3) from reviewer-code", 2
	return job
}

// walkTo feeds evs to job in the default pipeline, failing on any error.
func walkTo(ctx *specs.Context, job domain.Job, evs ...domain.Event) domain.Job {
	step := walker(ctx, &job)
	for _, ev := range evs {
		step(ev)
	}
	return job
}

// reviewedBy passes every reviewer but the last, which answers with last.
func reviewedBy(last domain.ResultSubmitted) []domain.Event {
	var evs []domain.Event
	for _, role := range reviewers[:len(reviewers)-1] {
		evs = append(evs, pass(role, 5))
	}
	return append(evs, last)
}

func TestWaitSeq(t *testing.T) {
	specs.Describe(t, "the job's WaitSeq", func(s *specs.Spec) {
		s.It("starts at 0", func(ctx *specs.Context) {
			ctx.Expect(domain.Job{}.WaitSeq).ToEqual(0)
			ctx.Expect(newJob().WaitSeq).ToEqual(0)
		})

		s.When("the job enters a wait", func(s *specs.Spec) {
			var rows []seqRow
			for _, r := range causeRows() {
				rows = append(rows, seqRow{"rises by one when " + r.name, r.job, r.ev})
			}
			rows = append(rows,
				seqRow{"rises by one when intake asks a question", newJob,
					result("intake", 1, domain.IntakeResult{Decision: domain.IntakeNeedsInfo})},
				seqRow{"rises by one when the merge completes", func() domain.Job {
					job := newJob()
					job.Stage = domain.StageMerge
					return job
				}, merged(1)},
			)
			specs.Table(s, rows, func(r seqRow) string { return r.name }, func(ctx *specs.Context, r seqRow) {
				job := r.job()
				job.WaitSeq = 4

				after := walkTo(ctx, job, r.ev)

				ctx.Expect(waitingStage(after.Stage)).To(specs.BeTrue())
				ctx.Expect(after.WaitSeq).ToEqual(5)
			})
		})

		s.When("the job leaves a wait or moves outside one", func(s *specs.Spec) {
			s.It("keeps it after feedback, an OK, an override, a stop or a direction in needs-human", func(ctx *specs.Context) {
				for _, ev := range []domain.Event{
					answering(message(domain.AuthorPerson, true, 2), 2),
					domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 2, At: at(2)},
					func() domain.Event { o := override(domain.AuthorPerson, true, 2); o.WaitSeq = 2; return o }(),
					stop("pablogore", 2),
					answering(direction(domain.AuthorPerson, true, 2), 2),
				} {
					after := walkTo(ctx, secondWait(), ev)

					ctx.Expect(waitingStage(after.Stage)).To(specs.BeFalse())
					ctx.Expect(after.WaitSeq).ToEqual(2)
				}
			})

			s.It("keeps it after an answer in needs-info and a reopening in done", func(ctx *specs.Context) {
				ctx.Expect(walkTo(ctx, askingJob(), message(domain.AuthorPerson, true, 2)).WaitSeq).ToEqual(1)
				ctx.Expect(walkTo(ctx, doneJob(), message(domain.AuthorPerson, true, 2)).WaitSeq).ToEqual(1)
			})

			s.It("keeps it through stages and rounds outside a wait", func(ctx *specs.Context) {
				job := building(1)
				job.WaitSeq = 3

				after := walkTo(ctx, job,
					result("builder", 1, domain.BuildResult{Status: domain.BuildReady}),
					gateFailed(2),
					result("builder", 3, domain.BuildResult{Status: domain.BuildReady}),
					domain.GatePassed{Job: jobID, At: at(4)},
				)

				ctx.Expect(after.Stage).ToEqual("review")
				ctx.Expect(after.Round).ToEqual(2)
				ctx.Expect(after.WaitSeq).ToEqual(3)
			})

			s.It("keeps it when the job is rejected or dropped", func(ctx *specs.Context) {
				job := secondWait()

				for _, ev := range []domain.Event{
					domain.JobRejected{Job: jobID, Reason: "out of scope", At: at(2)},
					domain.SliceDropped{Job: jobID, Initiative: initiativeID, By: "pablogore", Text: directionText, At: at(2)},
				} {
					ctx.Expect(decide.Apply(job, []domain.Event{ev}).WaitSeq).ToEqual(2)
				}
			})
		})
	})
}

func TestStaleAction(t *testing.T) {
	specs.Describe(t, "Decide on a person's action for another wait", func(s *specs.Spec) {
		s.When("it names a wait other than the job's", func(s *specs.Spec) {
			var rows []staleRow
			for _, a := range actions() {
				rows = append(rows,
					staleRow{"refuses " + a.name + " for the previous wait, naming the expected one", a.ev(1),
						"action for another wait: " + a.ev(1).EventName() + " for wait 1, expected wait 2"},
					staleRow{"refuses " + a.name + " for a later wait, naming the expected one", a.ev(3),
						"action for another wait: " + a.ev(3).EventName() + " for wait 3, expected wait 2"},
				)
			}
			specs.Table(s, rows, func(r staleRow) string { return r.name }, func(ctx *specs.Context, r staleRow) {
				job := secondWait()

				evs, err := decide.Decide(job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				ctx.Expect(err.Error()).ToEqual(r.text)
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("it names no wait", func(s *specs.Spec) {
			specs.Table(s, actions(), func(a action) string { return "refuses " + a.name + " as a missing field" }, func(ctx *specs.Context, a action) {
				evs, err := decide.Decide(secondWait(), a.ev(0), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
				ctx.Expect(err).To(specs.Not(specs.MatchError(decide.ErrStaleWait)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("it names the job's wait", func(s *specs.Spec) {
			specs.Table(s, actions(), func(a action) string { return a.name + " acts as before" }, func(ctx *specs.Context, a action) {
				evs, err := decide.Decide(secondWait(), a.ev(2), defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).To(specs.Not(specs.BeEmpty()))
			})
		})

		s.When("other refusals come first", func(s *specs.Spec) {
			stopped, rejected := stoppedJob(), unmergedSlice(domain.StageRejected)
			stopped.WaitSeq, rejected.WaitSeq = 2, 2
			var rows []orderRow
			for _, a := range actions() {
				rows = append(rows,
					orderRow{"a stopped job refuses " + a.name + " for another wait as stopped", stopped, a.ev(1), decide.ErrStopped},
					orderRow{"a rejected job refuses " + a.name + " for another wait as illegal", rejected, a.ev(1), decide.ErrIllegalTransition},
				)
			}
			blankOK := domain.HumanApproved{Job: jobID, By: " ", WaitSeq: 1, At: at(2)}
			blankOverride := override(domain.AuthorPerson, true, 2)
			blankOverride.By = ""
			noReason := override(domain.AuthorPerson, true, 2)
			noReason.Reason = " "
			blankMessage := signed(message(domain.AuthorPerson, true, 2), "")
			rows = append(rows,
				orderRow{"refuses an OK for another wait with no name for the name", secondWait(), blankOK, domain.ErrMissingField},
				orderRow{"refuses an override for another wait with no name for the name", secondWait(), blankOverride, domain.ErrMissingField},
				orderRow{"refuses an override for another wait with no reason for the reason", secondWait(), noReason, domain.ErrMissingField},
				orderRow{"refuses a message for another wait with no name for the name", secondWait(), blankMessage, domain.ErrMissingField},
			)
			specs.Table(s, rows, func(r orderRow) string { return r.name }, func(ctx *specs.Context, r orderRow) {
				evs, err := decide.Decide(r.job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(r.want))
				ctx.Expect(err).To(specs.Not(specs.MatchError(decide.ErrStaleWait)))
				ctx.Expect(evs).To(specs.BeEmpty())
			})

			s.It("ignores an override or a message for another wait from a bot or a person not allowed", func(ctx *specs.Context) {
				for _, ev := range []domain.Event{
					override(domain.AuthorBot, true, 2),
					override(domain.AuthorPerson, false, 2),
					message(domain.AuthorBot, true, 2),
					message(domain.AuthorPerson, false, 2),
				} {
					evs, err := decide.Decide(secondWait(), ev, defaultPipeline())

					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(evs).To(specs.BeEmpty())
				}
			})
		})

		s.When("the job is in any stage", func(s *specs.Spec) {
			stages := []stageRow{
				{"build", "build"}, {"review", "review"}, {"approve", "approve"}, {"merge", domain.StageMerge},
				{"needs-info", domain.StageNeedsInfo}, {"done", domain.StageDone},
			}
			specs.Table(s, stages, func(r stageRow) string {
				return "refuses an action for another wait in " + r.name + " as in needs-human, telling nothing about the stage"
			}, func(ctx *specs.Context, r stageRow) {
				for _, a := range actions() {
					want := secondWait()
					_, wantErr := decide.Decide(want, a.ev(1), defaultPipeline())
					job := newJob()
					job.Stage, job.WaitSeq, job.InfoFrom = r.stage, 2, "intake"

					evs, err := decide.Decide(job, a.ev(1), defaultPipeline())

					ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
					ctx.Expect(err.Error()).ToEqual(wantErr.Error())
					ctx.Expect(evs).To(specs.BeEmpty())

					_, err = decide.Decide(job, a.ev(0), defaultPipeline())
					_, wantErr = decide.Decide(want, a.ev(0), defaultPipeline())
					ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
					ctx.Expect(err.Error()).ToEqual(wantErr.Error())
				}
			})
		})

		s.When("a job is loaded in a wait with WaitSeq 0", func(s *specs.Spec) {
			escalated := needsHumanJob()
			escalated.Cause = domain.CauseEscalated
			changed := needsHumanJob()
			changed.Cause = domain.CauseChanges
			jobs := []struct {
				name string
				job  domain.Job
			}{{"needs-human after ESCALATE", escalated}, {"needs-human after CHANGES", changed}, {"needs-info", askingJob()}, {"done", doneJob()}}
			var rows []loadedRow
			for _, j := range jobs {
				j.job.WaitSeq = 0
				for _, a := range actions() {
					for _, seq := range []int{0, 1, 2} {
						rows = append(rows, loadedRow{"refuses " + a.name + " for wait " + strconv.Itoa(seq) + " in " + j.name, j.job, a.ev(seq)})
					}
				}
			}
			specs.Table(s, rows, func(r loadedRow) string { return r.name }, func(ctx *specs.Context, r loadedRow) {
				evs, err := decide.Decide(r.job, r.ev, defaultPipeline())

				ctx.Expect(err).To(specs.Not(specs.BeNil()))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("a direction drops a slice", func(s *specs.Spec) {
			s.It("drops it outside a wait whatever wait it names, since the drop ends the job", func(ctx *specs.Context) {
				for _, seq := range []int{0, 1, 2} {
					evs, err := decide.Decide(unmergedSlice("build"), answering(direction(domain.AuthorPerson, true, 1), seq), defaultPipeline())

					ctx.Expect(err).To(specs.BeNil())
					ctx.Expect(evs).ToEqual(droppedBy(1))
				}
			})

			s.It("refuses it in a wait when it names another wait", func(ctx *specs.Context) {
				job := unmergedSlice(domain.StageNeedsHuman)
				job.WaitSeq = 2

				evs, err := decide.Decide(job, direction(domain.AuthorPerson, true, 1), defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				ctx.Expect(evs).To(specs.BeEmpty())
			})
		})

		s.When("an action for an earlier wait is delivered again", func(s *specs.Spec) {
			s.It("refuses the old OK after an approver ESCALATE and merges on the current one", func(ctx *specs.Context) {
				job := walkTo(ctx, building(2), blockedBuild("cannot reach the payments API", 1))
				ctx.Expect(job.WaitSeq).ToEqual(1)
				old := domain.HumanApproved{Job: jobID, By: "pablogore", WaitSeq: 1, At: at(2)}
				job = walkTo(ctx, job,
					answering(message(domain.AuthorPerson, true, 2), 1),
					result("builder", 3, domain.BuildResult{Status: domain.BuildReady}),
					domain.GatePassed{Job: jobID, At: at(4)},
				)
				job = walkTo(ctx, job, reviewedBy(pass(reviewers[len(reviewers)-1], 5))...)
				job = walkTo(ctx, job, result("approver", 6, domain.ApproveResult{Decision: domain.ApproveEscalate, Reasons: []string{"too risky"}}))
				ctx.Expect(job.Cause).ToEqual(domain.CauseEscalated)
				ctx.Expect(job.WaitSeq).ToEqual(2)

				evs, err := decide.Decide(job, old, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				ctx.Expect(evs).To(specs.BeEmpty())

				current := old
				current.WaitSeq, current.At = 2, at(7)
				evs, err = decide.Decide(job, current, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.MergeRequested{Job: jobID, At: at(7)}})
			})

			s.It("refuses the old override after a second CHANGES in the last round and merges on the current one", func(ctx *specs.Context) {
				toReview := []domain.Event{
					result("builder", 3, domain.BuildResult{Status: domain.BuildReady}),
					domain.GatePassed{Job: jobID, At: at(4)},
				}
				job := walkTo(ctx, building(3), toReview...)
				job = walkTo(ctx, job, reviewedBy(changes(reviewers[len(reviewers)-1], 5))...)
				ctx.Expect(job.Cause).ToEqual(domain.CauseChanges)
				ctx.Expect(job.WaitSeq).ToEqual(1)
				old := override(domain.AuthorPerson, true, 6)
				job = walkTo(ctx, job, answering(message(domain.AuthorPerson, true, 6), 1))
				job = walkTo(ctx, job, toReview...)
				job = walkTo(ctx, job, reviewedBy(changes(reviewers[len(reviewers)-1], 5))...)
				ctx.Expect(job.Cause).ToEqual(domain.CauseChanges)
				ctx.Expect(job.WaitSeq).ToEqual(2)

				evs, err := decide.Decide(job, old, defaultPipeline())

				ctx.Expect(err).To(specs.MatchError(decide.ErrStaleWait))
				ctx.Expect(evs).To(specs.BeEmpty())

				current := old
				current.WaitSeq = 2
				evs, err = decide.Decide(job, current, defaultPipeline())

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual(overrideMerges(6))
			})
		})
	})
}

// waitingStage reports whether stage waits on a person.
func waitingStage(stage string) bool {
	return stage == domain.StageNeedsHuman || stage == domain.StageNeedsInfo || stage == domain.StageDone
}
