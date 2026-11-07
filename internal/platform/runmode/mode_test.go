package runmode_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/runmode"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
)

// patience bounds how long a test waits for an agent to start or a result
// to arrive, so a broken brake fails instead of hanging.
const patience = 2 * time.Second

var (
	epoch          = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	errAgentFailed = errors.New("agent failed")
)

// runner is a fake queue.Runner. The agent of each issue, once started,
// waits until the test finishes it; runner keeps the order they started in.
type runner struct {
	mu      sync.Mutex
	order   []int
	started map[int]chan struct{}
	finish  map[int]chan error
	results map[int]chan error
}

func newRunner() *runner {
	return &runner{started: map[int]chan struct{}{}, finish: map[int]chan error{}, results: map[int]chan error{}}
}

func (r *runner) chans(n int) (chan struct{}, chan error, chan error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started[n] == nil {
		r.started[n], r.finish[n], r.results[n] = make(chan struct{}), make(chan error), make(chan error, 1)
	}
	return r.started[n], r.finish[n], r.results[n]
}

func (r *runner) Work(is queue.Issue) (domain.JobID, scheduler.Work) {
	started, finish, _ := r.chans(is.Number)
	return domain.JobID(fmt.Sprint(is.Number)), func(context.Context) error {
		r.mu.Lock()
		r.order = append(r.order, is.Number)
		r.mu.Unlock()
		close(started)
		return <-finish
	}
}

func (r *runner) Done(is queue.Issue, err error) {
	_, _, results := r.chans(is.Number)
	results <- err
}

func (r *runner) taken() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int{}, r.order...)
}

func (r *runner) awaitStart(ctx *specs.Context, n int) {
	started, _, _ := r.chans(n)
	select {
	case <-started:
	case <-time.After(patience):
		ctx.Errorf("issue #%d did not start", n)
	}
}

// end finishes issue n's agent with err and waits for its result.
func (r *runner) end(ctx *specs.Context, n int, err error) error {
	_, finish, results := r.chans(n)
	finish <- err
	select {
	case got := <-results:
		return got
	case <-time.After(patience):
		ctx.Errorf("issue #%d did not finish", n)
		return nil
	}
}

type costs struct{ spent float64 }

func (c costs) Spent(context.Context, domain.JobID) (float64, error) { return c.spent, nil }

func (c costs) Budget(context.Context, domain.JobID) (float64, error) { return 0, nil }

// plenty is a fake GitHub limit source with plenty of calls left.
type plenty struct{}

func (plenty) Limit(context.Context) (scheduler.RateLimit, error) {
	return scheduler.RateLimit{Remaining: 5000, Reset: epoch.Add(time.Hour)}, nil
}

type stopped struct{}

func (stopped) Now() time.Time                       { return epoch }
func (stopped) After(time.Duration) <-chan time.Time { return nil }

func newScheduler(ctx *specs.Context, concurrency int, c costs) *scheduler.Scheduler {
	s, err := scheduler.New(concurrency, 5, c, scheduler.GitHub{Limits: plenty{}, Threshold: 100, Clock: stopped{}})
	ctx.Expect(err).To(specs.BeNil())
	return s
}

// reports is a fake runmode.Reports: every job maps to its issue's report.
type reports map[domain.JobID]runmode.IssueReport

func (r reports) Report(_ context.Context, job domain.JobID) (runmode.IssueReport, error) {
	rep, ok := r[job]
	if !ok {
		return runmode.IssueReport{}, ports.ErrJobNotFound
	}
	return rep, nil
}

func (r reports) add(rep runmode.IssueReport) reports {
	for _, j := range rep.Jobs {
		r[j.Job.ID] = rep
	}
	return r
}

// outbox is a fake runmode.Outbox that keeps an action once per key.
type outbox struct {
	mu   sync.Mutex
	acts []ports.QueuedAction
}

func (o *outbox) Enqueue(_ context.Context, job domain.JobID, acts ...ports.Action) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, a := range acts {
		if err := a.Validate(); err != nil {
			return err
		}
		known := false
		for _, q := range o.acts {
			known = known || q.Key == a.Key
		}
		if !known {
			o.acts = append(o.acts, ports.QueuedAction{Job: job, Action: a})
		}
	}
	return nil
}

func (o *outbox) saved() []ports.QueuedAction {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ports.QueuedAction{}, o.acts...)
}

// inbox is a fake runmode.Inbox.
type inbox struct {
	mu      sync.Mutex
	entries map[string]runmode.Summary
}

func (i *inbox) Put(_ context.Context, key string, s runmode.Summary) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.entries == nil {
		i.entries = map[string]runmode.Summary{}
	}
	i.entries[key] = s
	return nil
}

func (i *inbox) all() map[string]runmode.Summary {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := map[string]runmode.Summary{}
	for k, v := range i.entries {
		out[k] = v
	}
	return out
}

// initiative is issue #1 worked as two slices, the second job "1" being
// the one the queue started for it.
func initiative() runmode.IssueReport {
	origin := domain.Origin{Provider: "github", Thread: "issue/1", Author: "ana"}
	return runmode.IssueReport{
		Issue: queue.Issue{Number: 1, Title: "Run mode"},
		Jobs: []runmode.JobReport{
			{Job: domain.Job{ID: "1-s1", Title: "S1", Stage: domain.StageDone, Round: 2, Initiative: "i1", Origin: origin}, PR: "pull/10", USD: 1.5, Start: epoch, End: epoch.Add(time.Hour)},
			{Job: domain.Job{ID: "1", Title: "S2", Stage: domain.StageDone, Round: 1, Initiative: "i1", Origin: origin}, PR: "pull/11", USD: 0.25, Start: epoch.Add(time.Hour), End: epoch.Add(3 * time.Hour)},
		},
	}
}

func alone(n int, stage string) runmode.IssueReport {
	id := domain.JobID(fmt.Sprint(n))
	return runmode.IssueReport{
		Issue: queue.Issue{Number: n, Title: fmt.Sprintf("Issue %d", n)},
		Jobs:  []runmode.JobReport{{Job: domain.Job{ID: id, Stage: stage, Round: 1}, Start: epoch, End: epoch.Add(time.Minute)}},
	}
}

// noDeps is a fake queue.Issues for issues that depend on none.
type noDeps struct{}

func (noDeps) Finished(_ context.Context, n int) (bool, error) {
	return false, fmt.Errorf("issue #%d: %w", n, queue.ErrNoIssue)
}

// open is a fake queue.Issues: the issues it lists are open, every other
// one is merged or closed.
type open []int

func (o open) Finished(_ context.Context, n int) (bool, error) {
	return !slices.Contains(o, n), nil
}

type fixture struct {
	r      *runner
	sched  *scheduler.Scheduler
	brake  *runmode.Brake
	q      *queue.Queue
	mode   *runmode.Mode
	outbox *outbox
	inbox  *inbox
}

func newFixture(ctx *specs.Context, order queue.Order, reps reports) fixture {
	return newFixtureOpen(ctx, order, reps, noDeps{})
}

// newFixtureOpen is newFixture with deps answering for the issues the queue
// depends on, so it holds those depending on open ones.
func newFixtureOpen(ctx *specs.Context, order queue.Order, reps reports, deps queue.Issues) fixture {
	f := fixture{r: newRunner(), sched: newScheduler(ctx, 1, costs{}), outbox: &outbox{}, inbox: &inbox{}}
	f.brake = runmode.NewBrake(f.sched)
	f.q = queue.New(f.brake, order, deps, f.r)
	f.mode = runmode.New(f.brake, f.q, order, reps, f.outbox, f.inbox)
	return f
}

func waitingNumbers(q *queue.Queue) []int {
	out := []int{}
	for _, is := range q.Waiting() {
		out = append(out, is.Number)
	}
	return out
}

func TestMode(t *testing.T) {
	specs.Describe(t, "runmode.Mode", func(s *specs.Spec) {
		s.When("an initiative ends DONE in the default mode", func(s *specs.Spec) {
			s.It("starts no new issue, runs a slice of a running initiative and publishes the summary", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{TechLead: []int{1, 2}}, reports{}.add(initiative()))
				bg := context.Background()
				f.q.Add(bg, queue.Issue{Number: 1, Title: "Run mode"}, queue.Issue{Number: 2, Title: "Next one"}, queue.Issue{Number: 3})
				f.r.awaitStart(ctx, 1)

				ctx.Expect(f.mode.Handle(bg, domain.InitiativeEnded{Job: "1", Initiative: "i1", At: epoch.Add(3 * time.Hour)})).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeTrue())

				// What already runs ends.
				ctx.Expect(f.r.end(ctx, 1, nil)).To(specs.BeNil())
				// A slice of another initiative goes to the scheduler
				// directly; it was handed over after issue #2, so once it
				// ran #2 had its turn.
				slice := f.sched.Submit(bg, "7-s2", func(context.Context) error { return nil })
				select {
				case err := <-slice:
					ctx.Expect(err).To(specs.BeNil())
				case <-time.After(patience):
					ctx.Errorf("the slice did not run")
				}
				ctx.Expect(f.r.taken()).ToEqual([]int{1})
				ctx.Expect(waitingNumbers(f.q)).ToEqual([]int{2, 3})

				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Job).ToEqual(domain.JobID("1"))
				ctx.Expect(acts[0].Kind).ToEqual(runmode.ActionSummary)
				var posted struct {
					Origin  domain.Origin   `json:"origin"`
					Summary runmode.Summary `json:"summary"`
				}
				ctx.Expect(json.Unmarshal([]byte(acts[0].Payload), &posted)).To(specs.BeNil())
				ctx.Expect(posted.Origin.Thread).ToEqual("issue/1")
				want := runmode.Summary{
					Issue: 1, Title: "Run mode", Stage: domain.StageDone,
					PRs: []string{"pull/10", "pull/11"},
					Slices: []runmode.Slice{
						{Job: "1-s1", Title: "S1", Stage: domain.StageDone, Rounds: 2},
						{Job: "1", Title: "S2", Stage: domain.StageDone, Rounds: 1},
					},
					Rounds: 3, USD: 1.75, Duration: 3 * time.Hour,
					Next:    &runmode.Next{Issue: 2, Title: "Next one", Why: domain.NextTechLead},
					Options: []runmode.Option{runmode.OptionContinue, runmode.OptionPick, runmode.OptionReplan, runmode.OptionStop},
				}
				ctx.Expect(posted.Summary).ToEqual(want)
				ctx.Expect(f.inbox.all()).ToEqual(map[string]runmode.Summary{acts[0].Key: want})
			})

			s.It("starts the held issue and the next ones once released", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{}, reports{}.add(initiative()))
				bg := context.Background()
				f.q.Add(bg, queue.Issue{Number: 1}, queue.Issue{Number: 2}, queue.Issue{Number: 3})
				f.r.awaitStart(ctx, 1)
				ctx.Expect(f.mode.Handle(bg, domain.InitiativeEnded{Job: "1", Initiative: "i1", At: epoch})).To(specs.BeNil())
				ctx.Expect(f.r.end(ctx, 1, nil)).To(specs.BeNil())

				f.mode.Release(bg)
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())
				f.r.awaitStart(ctx, 2)
				ctx.Expect(f.r.end(ctx, 2, nil)).To(specs.BeNil())
				f.r.awaitStart(ctx, 3)
				ctx.Expect(f.r.end(ctx, 3, nil)).To(specs.BeNil())
				ctx.Expect(f.r.taken()).ToEqual([]int{1, 2, 3})
			})

			s.It("publishes the summary once when the event is handed over again", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{}, reports{}.add(initiative()))
				ev := domain.InitiativeEnded{Job: "1", Initiative: "i1", At: epoch}
				ctx.Expect(f.mode.Handle(context.Background(), ev)).To(specs.BeNil())
				ctx.Expect(f.mode.Handle(context.Background(), ev)).To(specs.BeNil())
				ctx.Expect(len(f.outbox.saved())).ToEqual(1)
				ctx.Expect(len(f.inbox.all())).ToEqual(1)
			})
		})

		s.When("the brake is on before an issue is handed over", func(s *specs.Spec) {
			s.It("hands the queue's issues nothing", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{}, reports{}.add(alone(5, domain.StageNeedsHuman)))
				bg := context.Background()
				ctx.Expect(f.mode.Handle(bg, domain.NeedsHuman{Job: "5", Reason: "blocked", Cause: domain.CauseBlocked, At: epoch})).To(specs.BeNil())
				f.q.Add(bg, queue.Issue{Number: 1})
				ctx.Expect(f.sched.Waiting()).ToEqual(0)
				ctx.Expect(waitingNumbers(f.q)).ToEqual([]int{1})
				f.mode.Release(bg)
				f.r.awaitStart(ctx, 1)
				ctx.Expect(f.r.end(ctx, 1, nil)).To(specs.BeNil())
			})
		})

		s.When("an initiative goes to needs-human in the default mode", func(s *specs.Spec) {
			s.It("starts no new issue and publishes the same summary as the other brakes", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{TechLead: []int{1, 2}}, reports{}.add(initiative()))
				bg := context.Background()
				f.q.Add(bg, queue.Issue{Number: 1, Title: "Run mode"}, queue.Issue{Number: 2, Title: "Next one"})
				f.r.awaitStart(ctx, 1)

				ev := domain.InitiativeNeedsHumanRaised{Job: "1", Initiative: "i1", Reason: "the replan needs a decision", At: epoch.Add(3 * time.Hour)}
				ctx.Expect(f.mode.Handle(bg, ev)).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeTrue())
				ctx.Expect(f.r.end(ctx, 1, nil)).To(specs.BeNil())
				ctx.Expect(f.r.taken()).ToEqual([]int{1})
				ctx.Expect(waitingNumbers(f.q)).ToEqual([]int{2})

				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Kind).ToEqual(runmode.ActionSummary)
				var posted struct {
					Origin  domain.Origin   `json:"origin"`
					Summary runmode.Summary `json:"summary"`
				}
				ctx.Expect(json.Unmarshal([]byte(acts[0].Payload), &posted)).To(specs.BeNil())
				ctx.Expect(posted.Origin.Thread).ToEqual("issue/1")
				ctx.Expect(posted.Summary).ToEqual(runmode.Summarize(initiative(), "1", []queue.Issue{{Number: 2, Title: "Next one"}}, nil, queue.Order{TechLead: []int{1, 2}}))
				ctx.Expect(posted.Summary.Next).ToEqual(&runmode.Next{Issue: 2, Title: "Next one", Why: domain.NextTechLead})
				ctx.Expect(f.inbox.all()).ToEqual(map[string]runmode.Summary{acts[0].Key: posted.Summary})
			})
		})

		s.When("the queue holds the first issue waiting", func(s *specs.Spec) {
			s.It("recommends the next one it can take", func(ctx *specs.Context) {
				f := newFixtureOpen(ctx, queue.Order{TechLead: []int{1, 2, 3}}, reports{}.add(alone(1, domain.StageNeedsHuman)), open{50})
				bg := context.Background()
				f.q.Add(bg, queue.Issue{Number: 1}, queue.Issue{Number: 2, Title: "Held", Body: "Depends on #50"})
				f.r.awaitStart(ctx, 1)
				// #2 is held, so the queue handed nothing over; with the
				// brake already on, #3 is not handed over either and waits
				// behind #2.
				f.brake.Engage()
				f.q.Add(bg, queue.Issue{Number: 3, Title: "Free"})
				ctx.Expect(waitingNumbers(f.q)).ToEqual([]int{2, 3})
				ctx.Expect(f.mode.Handle(bg, domain.NeedsHuman{Job: "1", Reason: "blocked", Cause: domain.CauseBlocked, At: epoch})).To(specs.BeNil())

				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Payload).To(specs.Contain(`"next":{"issue":3,"title":"Free","why":"tech-lead-order"}`))
				ctx.Expect(f.inbox.all()[acts[0].Key].Next).ToEqual(&runmode.Next{Issue: 3, Title: "Free", Why: domain.NextTechLead})
				ctx.Expect(f.r.end(ctx, 1, nil)).To(specs.BeNil())
			})
		})

		s.When("no waiting issue can be taken", func(s *specs.Spec) {
			s.It("says every waiting issue is held and recommends none", func(ctx *specs.Context) {
				f := newFixtureOpen(ctx, queue.Order{}, reports{}.add(alone(1, domain.StageNeedsHuman)), open{50})
				bg := context.Background()
				f.q.Add(bg, queue.Issue{Number: 1}, queue.Issue{Number: 2, Body: "Depends on #50"})
				f.r.awaitStart(ctx, 1)
				ctx.Expect(f.mode.Handle(bg, domain.NeedsHuman{Job: "1", Reason: "blocked", Cause: domain.CauseBlocked, At: epoch})).To(specs.BeNil())

				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Payload).To(specs.Contain(`"no_next":"all-held"`))
				got := f.inbox.all()[acts[0].Key]
				ctx.Expect(got.Next).To(specs.BeNil())
				ctx.Expect(got.NoNext).ToEqual(domain.NoNextHeld)
				ctx.Expect(f.r.end(ctx, 1, nil)).To(specs.BeNil())
			})

			s.It("says no issue waits and recommends none", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{}, reports{}.add(alone(1, domain.StageDone)))
				ctx.Expect(f.mode.Handle(context.Background(), domain.StageEntered{Job: "1", Stage: domain.StageDone, At: epoch})).To(specs.BeNil())
				acts := f.outbox.saved()
				ctx.Expect(len(acts)).ToEqual(1)
				ctx.Expect(acts[0].Payload).To(specs.Contain(`"no_next":"queue-empty"`))
				ctx.Expect(f.inbox.all()[acts[0].Key].NoNext).ToEqual(domain.NoNextEmpty)
			})
		})

		s.When("a slice is rejected", func(s *specs.Spec) {
			s.It("does not brake: its initiative replans", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{}, reports{}.add(initiative()))
				ctx.Expect(f.mode.Handle(context.Background(), domain.JobRejected{Job: "1", Reason: "dup", At: epoch})).To(specs.BeNil())
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())
				ctx.Expect(f.outbox.saved()).To(specs.BeEmpty())
			})
		})

		s.When("the report of the job cannot be read", func(s *specs.Spec) {
			s.It("returns the error, so the bus retries", func(ctx *specs.Context) {
				f := newFixture(ctx, queue.Order{}, reports{})
				err := f.mode.Handle(context.Background(), domain.NeedsInfo{Job: "9", From: "intake", At: epoch})
				ctx.Expect(errors.Is(err, ports.ErrJobNotFound)).To(specs.BeTrue())
				ctx.Expect(f.brake.Engaged()).To(specs.BeFalse())
			})
		})
	})
}

type brakeRow struct {
	name string
	ev   domain.Event
	job  domain.Job
	want bool
}

func TestBrakes(t *testing.T) {
	specs.Describe(t, "runmode.Brakes", func(s *specs.Spec) {
		slice := domain.Job{ID: "j", Initiative: "i1"}
		own := domain.Job{ID: "j"}
		rows := []brakeRow{
			{"initiative DONE", domain.InitiativeEnded{Job: "j", Initiative: "i1"}, slice, true},
			{"slice in needs-human", domain.NeedsHuman{Job: "j", Cause: domain.CauseBlocked}, slice, true},
			{"job in needs-human", domain.NeedsHuman{Job: "j", Cause: domain.CauseBudget}, own, true},
			{"job in needs-info", domain.NeedsInfo{Job: "j", From: "intake"}, own, true},
			{"issue rejected", domain.JobRejected{Job: "j"}, own, true},
			{"job on its own done", domain.StageEntered{Job: "j", Stage: domain.StageDone}, own, true},
			{"slice rejected", domain.JobRejected{Job: "j"}, slice, false},
			{"slice done", domain.StageEntered{Job: "j", Stage: domain.StageDone}, slice, false},
			{"another stage", domain.StageEntered{Job: "j", Stage: "build"}, own, false},
			{"initiative in needs-human", domain.InitiativeNeedsHumanRaised{Job: "j", Initiative: "i1"}, slice, true},
		}
		specs.Table(s, rows, func(r brakeRow) string { return r.name }, func(ctx *specs.Context, r brakeRow) {
			ctx.Expect(runmode.Brakes(r.ev, r.job)).ToEqual(r.want)
		})
	})
}
