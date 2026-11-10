package runmode_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/runmode"
)

func newAutoFixture(ctx *specs.Context, l runmode.Limits, reps reports) fixture {
	f := newFixture(ctx, queue.Order{}, reps)
	m, err := runmode.NewAuto(context.Background(), f.brake, f.q, queue.Order{}, reps, f.outbox, f.inbox, f.log, l)
	ctx.Expect(err).To(specs.BeNil())
	f.mode = m
	return f
}

// costing is issue n on its own, its job "n" at stage, having cost usd.
func costing(n int, stage string, usd float64) runmode.IssueReport {
	r := alone(n, stage)
	r.Jobs[0].USD = usd
	return r
}

// turn waits until the scheduler has given every work handed over before
// it a slot: it runs last, first come first served.
func turn(ctx *specs.Context, f fixture) {
	select {
	case err := <-f.sched.Submit(context.Background(), "turn", func(context.Context) error { return nil }):
		ctx.Expect(err).To(specs.BeNil())
	case <-time.After(patience):
		ctx.Errorf("the scheduler did not get to the turn")
	}
}

// runStopWords is an example channel's wording of every run cap, by
// language. The real channels come with E07.
var runStopWords = map[string]map[domain.RunCap]string{
	"es": {
		domain.CapRunIssues: "la corrida terminó %v issues, el tope es %v",
		domain.CapRunUSD:    "la corrida gastó USD %v, el tope es USD %v",
	},
	"en": {
		domain.CapRunIssues: "the run finished %v issues, the cap is %v",
		domain.CapRunUSD:    "the run spent USD %v, the cap is USD %v",
	},
}

// autoRow is an event handed to the auto mode, and the report of its issue.
type autoRow struct {
	name string
	rep  runmode.IssueReport
	ev   domain.Event
}

func TestAuto(t *testing.T) {
	specs.Describe(t, "runmode.NewAuto", func(s *specs.Spec) {
		s.When("--max-jobs is 2 and three issues wait", func(s *specs.Spec) {
			s.It("starts two without braking, starts no third and tells the cap in the summary", func(ctx *specs.Context) {
				reps := reports{}.add(alone(1, domain.StageDone)).add(alone(2, domain.StageDone)).add(alone(3, domain.StageDone))
				f := newAutoFixture(ctx, runmode.Limits{Issues: 2, USD: 20}, reps)
				bg := context.Background()
				f.q.Add(bg, queue.Issue{Number: 1}, queue.Issue{Number: 2}, queue.Issue{Number: 3, Title: "Third"})

				// A job reaches done while its work still runs.
				f.r.awaitStart(ctx, 1)
				ctx.Expect(f.mode.Handle(bg, domain.StageEntered{Job: "1", Stage: domain.StageDone, At: epoch})).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())
				ctx.Expect(f.outbox.saved()).To(specs.BeEmpty())
				ctx.Expect(f.r.end(ctx, 1, nil)).To(specs.BeNil())

				f.r.awaitStart(ctx, 2)
				ctx.Expect(f.mode.Handle(bg, domain.StageEntered{Job: "2", Stage: domain.StageDone, At: epoch.Add(time.Minute)})).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeTrue())
				ctx.Expect(f.r.end(ctx, 2, nil)).To(specs.BeNil())

				turn(ctx, f)
				ctx.Expect(f.r.taken()).ToEqual([]int{1, 2})
				ctx.Expect(waitingNumbers(f.q)).ToEqual([]int{3})

				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Job).ToEqual(domain.JobID("2"))
				ctx.Expect(acts[0].Kind).ToEqual(runmode.ActionSummary)
				ctx.Expect(acts[0].Payload).To(specs.Contain(`"run_stop":{"cap":"run-issues","limit":2,"reached":2}`))
				var posted struct {
					Summary runmode.Summary `json:"summary"`
				}
				ctx.Expect(json.Unmarshal([]byte(acts[0].Payload), &posted)).To(specs.BeNil())
				want := runmode.Summarize(alone(2, domain.StageDone), "2", []queue.Issue{{Number: 3, Title: "Third"}}, nil, queue.Order{})
				want.RunStop = &runmode.RunStop{Cap: domain.CapRunIssues, Limit: 2, Reached: 2}
				ctx.Expect(posted.Summary).ToEqual(want)
				ctx.Expect(f.inbox.all()).ToEqual(map[string]runmode.Summary{acts[0].Key: want})
			})
		})

		s.When("an issue ends", func(s *specs.Spec) {
			rows := []autoRow{
				{"initiative DONE", initiative(), domain.InitiativeEnded{Job: "1", Initiative: "i1", At: epoch}},
				{"issue rejected", alone(1, domain.StageRejected), domain.JobRejected{Job: "1", Reason: "dup", At: epoch}},
				{"job on its own done", alone(1, domain.StageDone), domain.StageEntered{Job: "1", Stage: domain.StageDone, At: epoch}},
			}
			specs.Table(s, rows, func(r autoRow) string {
				return "does not brake on " + r.name
			}, func(ctx *specs.Context, r autoRow) {
				f := newAutoFixture(ctx, runmode.DefaultLimits, reports{}.add(r.rep))
				bg := context.Background()
				ctx.Expect(f.mode.Handle(bg, r.ev)).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())
				ctx.Expect(f.outbox.saved()).To(specs.BeEmpty())
				f.q.Add(bg, queue.Issue{Number: 2})
				f.r.awaitStart(ctx, 2)
				ctx.Expect(f.r.end(ctx, 2, nil)).To(specs.BeNil())
			})

			s.It("counts it once when the event is handed over again", func(ctx *specs.Context) {
				f := newAutoFixture(ctx, runmode.Limits{Issues: 2, USD: 20}, reports{}.add(alone(1, domain.StageDone)))
				ev := domain.StageEntered{Job: "1", Stage: domain.StageDone, At: epoch}
				ctx.Expect(f.mode.Handle(context.Background(), ev)).To(specs.BeNil())
				ctx.Expect(f.mode.Handle(context.Background(), ev)).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())
			})
		})

		s.When("the run's issues, running ones included, reach --max-usd", func(s *specs.Spec) {
			s.It("brakes on the event that crossed it and tells the cap and the USD reached", func(ctx *specs.Context) {
				reps := reports{}.add(costing(1, domain.StageDone, 2)).add(costing(2, "review", 1.5))
				f := newAutoFixture(ctx, runmode.Limits{Issues: 5, USD: 3}, reps)
				bg := context.Background()
				ctx.Expect(f.mode.Handle(bg, domain.StageEntered{Job: "1", Stage: domain.StageDone, At: epoch})).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())

				crossing := domain.StageEntered{Job: "2", Stage: "review", At: epoch.Add(time.Minute)}
				ctx.Expect(f.mode.Handle(bg, crossing)).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeTrue())
				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Payload).To(specs.Contain(`"run_stop":{"cap":"run-usd","limit":3,"reached":3.5}`))
				ctx.Expect(f.inbox.all()[acts[0].Key].RunStop).ToEqual(&runmode.RunStop{Cap: domain.CapRunUSD, Limit: 3, Reached: 3.5})

				// The crossing event again keeps one summary; another stage
				// of a running job publishes none.
				ctx.Expect(f.mode.Handle(bg, crossing)).To(specs.BeNil())
				ctx.Expect(f.mode.Handle(bg, domain.StageEntered{Job: "2", Stage: "review", At: epoch.Add(2 * time.Minute)})).To(specs.BeNil())
				ctx.Expect(len(f.outbox.saved())).ToEqual(1)
			})
		})

		s.When("the run is past a cap and a running issue ends", func(s *specs.Spec) {
			s.It("publishes its summary too", func(ctx *specs.Context) {
				reps := reports{}.add(alone(1, domain.StageDone)).add(alone(2, domain.StageDone))
				f := newAutoFixture(ctx, runmode.Limits{Issues: 1, USD: 20}, reps)
				bg := context.Background()
				ctx.Expect(f.mode.Handle(bg, domain.StageEntered{Job: "1", Stage: domain.StageDone, At: epoch})).To(specs.BeNil())
				ctx.Expect(f.mode.Handle(bg, domain.StageEntered{Job: "2", Stage: domain.StageDone, At: epoch})).To(specs.BeNil())
				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(2)
				ctx.Expect(f.inbox.all()[acts[1].Key].RunStop).ToEqual(&runmode.RunStop{Cap: domain.CapRunIssues, Limit: 1, Reached: 2})
			})
		})

		s.When("a job or an initiative stops for a person", func(s *specs.Spec) {
			rows := []autoRow{
				{"needs-human", alone(1, domain.StageNeedsHuman), domain.NeedsHuman{Job: "1", Reason: "blocked", Cause: domain.CauseBlocked, At: epoch}},
				{"needs-info", alone(1, domain.StageNeedsInfo), domain.NeedsInfo{Job: "1", From: "intake", At: epoch}},
				{"initiative needs-human", initiative(), domain.InitiativeNeedsHumanRaised{Job: "1", Initiative: "i1", Reason: "replan", At: epoch}},
			}
			specs.Table(s, rows, func(r autoRow) string {
				return "brakes as the default mode on " + r.name
			}, func(ctx *specs.Context, r autoRow) {
				f := newAutoFixture(ctx, runmode.DefaultLimits, reports{}.add(r.rep))
				bg := context.Background()
				ctx.Expect(f.mode.Handle(bg, r.ev)).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeTrue())
				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(f.inbox.all()[acts[0].Key]).ToEqual(runmode.Summarize(r.rep, "1", nil, nil, queue.Order{}))
				f.q.Add(bg, queue.Issue{Number: 2})
				ctx.Expect(f.sched.Waiting()).ToEqual(0)
				ctx.Expect(waitingNumbers(f.q)).ToEqual([]int{2})
			})
		})

		s.When("an issue is past its own cap", func(s *specs.Spec) {
			s.It("starts no slice of it, and the run goes on with the other issues", func(ctx *specs.Context) {
				reps := reports{}.add(sliced(7, 1, 1, 1, 1, 1))
				f := newAutoFixture(ctx, runmode.DefaultLimits, reps)
				st := newStarter(ctx, f.sched, runmode.DefaultCaps, reps, f.outbox, &stops{})
				bg := context.Background()
				_, err := st.Start(bg, domain.NextSliceRequested{Job: "7-s5", Initiative: "7", At: epoch}, "7-s6", func(context.Context) error {
					ctx.Errorf("the slice started")
					return nil
				})
				ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())
				f.q.Add(bg, queue.Issue{Number: 2})
				f.r.awaitStart(ctx, 2)
				ctx.Expect(f.r.end(ctx, 2, nil)).To(specs.BeNil())
			})
		})

		s.When("the run sets a cap under 1", func(s *specs.Spec) {
			s.It("fails naming the cap", func(ctx *specs.Context) {
				for _, c := range []struct {
					limits runmode.Limits
					key    string
				}{
					{runmode.Limits{Issues: 0, USD: 20}, "max-jobs is 0"},
					{runmode.Limits{Issues: 5, USD: 0}, "max-usd is 0"},
					{runmode.Limits{Issues: -1, USD: 20}, "max-jobs is -1"},
				} {
					f := newFixture(ctx, queue.Order{}, reports{})
					m, err := runmode.NewAuto(context.Background(), f.brake, f.q, queue.Order{}, reports{}, f.outbox, f.inbox, f.log, c.limits)
					ctx.Expect(m).To(specs.BeNil())
					ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
					ctx.Expect(err.Error()).To(specs.Contain(c.key))
				}
			})
		})

		s.When("a channel words the run's cap", func(s *specs.Spec) {
			s.It("has words for every cap in every language", func(ctx *specs.Context) {
				for _, lang := range []string{"es", "en"} {
					for _, c := range domain.RunCaps() {
						ctx.Expect(runStopWords[lang][c]).To(specs.Not(specs.BeEmpty()))
					}
				}
				ctx.Expect(fmt.Sprintf(runStopWords["es"][domain.CapRunIssues], 2, 2)).ToEqual("la corrida terminó 2 issues, el tope es 2")
			})
		})
	})
}

type runCappedRow struct {
	name   string
	issues int
	usd    float64
	want   runmode.RunStop
	capped bool
}

func TestRunCapped(t *testing.T) {
	specs.Describe(t, "runmode.RunCapped", func(s *specs.Spec) {
		rows := []runCappedRow{
			{"under both caps", 4, 19.5, runmode.RunStop{}, false},
			{"at 5 issues", 5, 0, runmode.RunStop{Cap: domain.CapRunIssues, Limit: 5, Reached: 5}, true},
			{"at USD 20", 1, 20, runmode.RunStop{Cap: domain.CapRunUSD, Limit: 20, Reached: 20}, true},
			{"past both, issues first", 6, 25, runmode.RunStop{Cap: domain.CapRunIssues, Limit: 5, Reached: 6}, true},
		}
		specs.Table(s, rows, func(r runCappedRow) string { return r.name }, func(ctx *specs.Context, r runCappedRow) {
			got, capped := runmode.RunCapped(r.issues, r.usd, runmode.DefaultLimits)
			ctx.Expect(capped).ToEqual(r.capped)
			ctx.Expect(got).ToEqual(r.want)
		})
	})
}
