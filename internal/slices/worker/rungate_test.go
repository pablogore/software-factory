package worker_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
)

// gate answers every check with run, or with err, and records the jobs it
// was asked to check. With hold, a check of a job in it says it started on
// started and answers only once hold is closed or its ctx ends, then with
// the ctx's error, as a cancelled run does; stopped has the jobs whose
// check ended so. With linger, a cancelled check says so on cancelled and
// takes until linger is closed to stop, instead of a moment.
type gate struct {
	mu        sync.Mutex
	run       domain.ChecksRun
	err       error
	jobs      []domain.JobID
	stopped   []domain.JobID
	hold      map[domain.JobID]chan struct{}
	started   chan domain.JobID
	linger    chan struct{}
	cancelled chan domain.JobID
}

func (g *gate) Check(ctx context.Context, id domain.JobID) (domain.ChecksRun, error) {
	g.mu.Lock()
	g.jobs = append(g.jobs, id)
	hold := g.hold[id]
	g.mu.Unlock()
	if hold != nil {
		g.started <- id
		select {
		case <-hold:
		case <-ctx.Done():
			if g.linger != nil {
				g.cancelled <- id
				<-g.linger
			} else {
				time.Sleep(50 * time.Millisecond)
			}
			g.mu.Lock()
			g.stopped = append(g.stopped, id)
			g.mu.Unlock()
			return domain.ChecksRun{}, ctx.Err()
		}
	}
	return g.run, g.err
}

func (g *gate) stoppedJobs() []domain.JobID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]domain.JobID{}, g.stopped...)
}

func (g *gate) checked() []domain.JobID {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]domain.JobID{}, g.jobs...)
}

// holding makes g's checks of jobs wait until the returned func is called.
func (g *gate) holding(jobs ...domain.JobID) func() {
	g.hold, g.started = map[domain.JobID]chan struct{}{}, make(chan domain.JobID, len(jobs))
	for _, id := range jobs {
		g.hold[id] = make(chan struct{})
	}
	return func() {
		for _, h := range g.hold {
			close(h)
		}
	}
}

// lingering makes g's cancelled checks stop only once the returned func is
// called.
func (g *gate) lingering() func() {
	g.linger, g.cancelled = make(chan struct{}), make(chan domain.JobID, 1)
	return func() { close(g.linger) }
}

// runHeld starts token's run_gate on job, held by g, and returns once its
// check started.
func runHeld(w world, g *gate, token string, job domain.JobID) {
	go func() { _, _ = w.sub.RunGate(context.Background(), token, job) }()
	<-g.started
}

// submitting submits data with token on job in the background, and returns
// the channel its error comes on.
func submitting(w world, token string, job domain.JobID, data string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := w.sub.Submit(context.Background(), token, job, []byte(data))
		done <- err
	}()
	return done
}

// pending tells whether the submission done comes from has not answered.
func pending(done <-chan error) bool {
	return len(done) == 0
}

var gated = domain.ChecksRun{Command: "make check", ExitCode: 2, Output: "--- FAIL: TestSave", Violations: []string{"Makefile is protected"}}

// gating is the tools of a build role that may run the job's checks.
var gating = []string{"submit_result", "progress", "run_gate"}

// withGate is w with a gate answering run on its submitter.
func withGate(w world, run domain.ChecksRun) (world, *gate) {
	g := &gate{run: run}
	w.sub.Gate = g
	return w, g
}

type refusedGateRow struct {
	name  string
	grant jobtoken.Grant
}

func TestRunGate(t *testing.T) {
	specs.Describe(t, "running the job's checks with a job token", func(s *specs.Spec) {
		s.It("returns the run of its job's checks to a builder that lists run_gate, saving nothing", func(ctx *specs.Context) {
			w, g := withGate(newWorld(ctx), gated)
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})

			run, err := w.sub.RunGate(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run).ToEqual(gated)
			ctx.Expect(g.checked()).ToEqual([]domain.JobID{"j-1"})
			w.untouched(ctx, "j-1")
			w.untouched(ctx, "j-2")
		})

		s.It("keeps the token, so the builder runs the checks again and still submits its result", func(ctx *specs.Context) {
			w, g := withGate(newWorld(ctx), gated)
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})

			_, first := w.sub.RunGate(context.Background(), token, "j-1")
			_, second := w.sub.RunGate(context.Background(), token, "j-1")
			_, submitted := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"status":"READY"}`))

			ctx.Expect(first).To(specs.BeNil())
			ctx.Expect(second).To(specs.BeNil())
			ctx.Expect(submitted).To(specs.BeNil())
			ctx.Expect(g.checked()).To(specs.HaveLen(2))
		})

		refused := []refusedGateRow{
			{"a builder without a tools list", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1}},
			{"a builder whose tools do not list run_gate", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: []string{"submit_result", "progress"}, Round: 1}},
			{"a reviewer without a tools list", jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Round: 1}},
			{"a reviewer that lists run_gate", jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Tools: gating, Round: 1}},
			{"an approver that lists run_gate", jobtoken.Grant{Job: "j-1", Role: "approver", Kind: domain.KindApprove, Tools: gating, Round: 1}},
		}
		specs.Table(s, refused, func(r refusedGateRow) string { return "refuses " + r.name + ", running no check" }, func(ctx *specs.Context, r refusedGateRow) {
			w, g := withGate(newWorld(ctx), gated)
			token := w.issue(ctx, r.grant)

			run, err := w.sub.RunGate(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(err.Error()).To(specs.Contain(`may not use tool "run_gate"`))
			ctx.Expect(run).ToEqual(domain.ChecksRun{})
			ctx.Expect(g.checked()).To(specs.BeEmpty())
			w.untouched(ctx, "j-1")
		})

		s.When("telling whether a token may run the checks, running none", func(s *specs.Spec) {
			s.It("lets a builder that lists run_gate on its own job", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				ctx.Expect(w.sub.MayRunGate(token, "j-1")).To(specs.BeTrue())
				ctx.Expect(w.sub.MayRunGate(token, "j-2")).To(specs.BeFalse())
				ctx.Expect(g.checked()).To(specs.BeEmpty())
			})

			s.It("does not let a reviewer, even one that lists run_gate, nor a call without a token", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Tools: gating, Round: 1})
				ctx.Expect(w.sub.MayRunGate(token, "j-1")).To(specs.BeFalse())
				ctx.Expect(w.sub.MayRunGate("", "j-1")).To(specs.BeFalse())
				ctx.Expect(g.checked()).To(specs.BeEmpty())
			})
		})

		s.When("the job's checks are already running", func(s *specs.Spec) {
			s.It("refuses a second call at once, saying so, and runs no other check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				release := g.holding("j-1")
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				first := make(chan error, 1)
				go func() {
					_, err := w.sub.RunGate(context.Background(), token, "j-1")
					first <- err
				}()
				<-g.started

				_, err := w.sub.RunGate(context.Background(), token, "j-1")
				release()

				ctx.Expect(err).To(specs.MatchError(domain.ErrBusy))
				ctx.Expect(err.Error()).ToEqual(`worker: run gate: already running: the checks of job "j-1" are running; call again once that run answers`)
				ctx.Expect(<-first).To(specs.BeNil())
				ctx.Expect(g.checked()).ToEqual([]domain.JobID{"j-1"})
			})

			s.It("still runs another job's checks meanwhile", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				release := g.holding("j-1")
				defer release()
				t1 := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				t2 := w.issue(ctx, jobtoken.Grant{Job: "j-2", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				go func() { _, _ = w.sub.RunGate(context.Background(), t1, "j-1") }()
				<-g.started

				_, err := w.sub.RunGate(context.Background(), t2, "j-2")

				ctx.Expect(err).To(specs.BeNil())
			})

			s.It("runs them again once the first run answered, even after it failed", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				g.err = errors.New("no make")
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				_, first := w.sub.RunGate(context.Background(), token, "j-1")
				g.err = nil
				_, second := w.sub.RunGate(context.Background(), token, "j-1")
				ctx.Expect(first).To(specs.Not(specs.BeNil()))
				ctx.Expect(second).To(specs.BeNil())
				ctx.Expect(g.checked()).To(specs.HaveLen(2))
			})
		})

		s.When("the job's result is submitted while its checks run", func(s *specs.Spec) {
			s.It("cancels the run, saves the result only once it stopped, and answers the run as cancelled, not failed", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				release := g.holding("j-1")
				defer release()
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				type answer struct {
					run domain.ChecksRun
					err error
				}
				first := make(chan answer, 1)
				go func() {
					run, err := w.sub.RunGate(context.Background(), token, "j-1")
					first <- answer{run, err}
				}()
				<-g.started

				evs, err := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"status":"READY"}`))

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.StageEntered{Job: "j-1", Stage: domain.StageGate, At: at}})
				ctx.Expect(g.stoppedJobs()).ToEqual([]domain.JobID{"j-1"})
				a := <-first
				ctx.Expect(a.err).To(specs.MatchError(gatelock.ErrPreempted))
				ctx.Expect(errors.Is(a.err, domain.ErrBusy)).To(specs.BeFalse())
				ctx.Expect(a.run).ToEqual(domain.ChecksRun{})
			})

			s.It("holds up no other job's submission or progress while it waits for the run to stop", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				release := g.holding("j-1")
				defer release()
				stop := g.lingering()
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				runHeld(w, g, token, "j-1")
				first := submitting(w, token, "j-1", `{"status":"READY"}`)
				<-g.cancelled

				var progressed, err error
				var evs []domain.Event
				other := make(chan struct{})
				go func() {
					_, progressed = w.sub.Progress(context.Background(), w.j2, "j-2", "still building")
					evs, err = w.sub.Submit(context.Background(), w.j2, "j-2", []byte(`{"status":"READY"}`))
					close(other)
				}()
				answered := false
				select {
				case <-other:
					answered = true
				case <-time.After(5 * time.Second):
				}

				ctx.Expect(answered).To(specs.BeTrue())
				if !answered {
					stop()
					<-other
				}
				ctx.Expect(progressed).To(specs.BeNil())
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(evs).ToEqual([]domain.Event{domain.StageEntered{Job: "j-2", Stage: domain.StageGate, At: at}})
				ctx.Expect(pending(first)).To(specs.BeTrue())
				w.untouched(ctx, "j-1")
				stop()
				ctx.Expect(<-first).To(specs.BeNil())
				job, _, err := w.store.Load(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(job.Stage).ToEqual(domain.StageGate)
				ctx.Expect(g.stoppedJobs()).ToEqual([]domain.JobID{"j-1"})
			})

			s.It("takes one of two submissions made at once with the same token and refuses the other", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				release := g.holding("j-1")
				defer release()
				stop := g.lingering()
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				runHeld(w, g, token, "j-1")
				a := submitting(w, token, "j-1", `{"status":"READY"}`)
				b := submitting(w, token, "j-1", `{"status":"READY"}`)
				<-g.cancelled
				stop()

				errs := []error{<-a, <-b}
				saved := 0
				for _, err := range errs {
					if err == nil {
						saved++
						continue
					}
					ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
				}
				ctx.Expect(saved).ToEqual(1)
				hist, err := w.store.History(context.Background(), "j-1")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(hist).To(specs.HaveLen(1))
				ctx.Expect(hist[0].Event).ToEqual(domain.StageEntered{Job: "j-1", Stage: domain.StageGate, At: at})
			})

			s.It("cancels no run for a result it refuses", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				release := g.holding("j-1")
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				first := make(chan error, 1)
				go func() {
					_, err := w.sub.RunGate(context.Background(), token, "j-1")
					first <- err
				}()
				<-g.started

				_, err := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"status":"DONE"}`))
				release()

				ctx.Expect(err).To(specs.MatchError(domain.ErrInvalidValue))
				ctx.Expect(<-first).To(specs.BeNil())
				ctx.Expect(g.stoppedJobs()).To(specs.BeEmpty())
			})
		})

		s.When("the token is not for this run", func(s *specs.Spec) {
			s.It("refuses another job's token, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-2", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				_, err := w.sub.RunGate(context.Background(), token, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
				ctx.Expect(g.checked()).To(specs.BeEmpty())
			})

			s.It("refuses a call without a token, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				_, err := w.sub.RunGate(context.Background(), "", "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrNoToken))
				ctx.Expect(g.checked()).To(specs.BeEmpty())
			})

			s.It("refuses a token from an earlier round, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				w.newRound(ctx, "j-1")
				_, err := w.sub.RunGate(context.Background(), token, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrStale))
				ctx.Expect(g.checked()).To(specs.BeEmpty())
			})

			s.It("refuses a token whose result was saved, running no check", func(ctx *specs.Context) {
				w, g := withGate(newWorld(ctx), gated)
				token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
				_, err := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"status":"READY"}`))
				ctx.Expect(err).To(specs.BeNil())
				_, err = w.sub.RunGate(context.Background(), token, "j-1")
				ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
				ctx.Expect(g.checked()).To(specs.BeEmpty())
			})
		})

		s.It("is an error when the checks cannot be run", func(ctx *specs.Context) {
			w, g := withGate(newWorld(ctx), gated)
			boom := errors.New("no make")
			g.err = boom
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
			_, err := w.sub.RunGate(context.Background(), token, "j-1")
			ctx.Expect(err).To(specs.MatchError(boom))
			w.untouched(ctx, "j-1")
		})

		s.It("is an error, not a panic, without a gate", func(ctx *specs.Context) {
			w := newWorld(ctx)
			token := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1})
			_, err := w.sub.RunGate(context.Background(), token, "j-1")
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
		})
	})
}
