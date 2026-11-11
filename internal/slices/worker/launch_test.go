package worker_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

// kindRoles reads a reviewer-* role as a review role and every other as a
// build role that lists run_gate in its tools, with a prompt naming it.
type kindRoles struct{}

func (kindRoles) Role(_ context.Context, name string) (domain.Role, string, error) {
	if strings.HasPrefix(name, "reviewer-") {
		return domain.Role{Name: name, Kind: domain.KindReview}, "prompt of " + name, nil
	}
	return domain.Role{Name: name, Kind: domain.KindBuild, Tools: builderTools}, "prompt of " + name, nil
}

// builderTools are the tools kindRoles' build roles list.
var builderTools = []string{"submit_result", "progress", "run_gate"}

// launchedAgents starts scripted sessions that submit turns with the token
// their MCP server was given, and records each session's spec; with fail
// set, no session starts, with hang set, every turn hangs, and with limited
// set, that turn ends on the agent's limit.
type launchedAgents struct {
	sub     *worker.Submitter
	turns   []string
	fail    error
	hang    bool
	limited int

	mu       sync.Mutex
	specs    []ports.SessionSpec
	sessions []*scripted
	gone     []chan struct{}
}

func (a *launchedAgents) Start(_ context.Context, _ domain.Role, s ports.SessionSpec) (ports.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.specs = append(a.specs, s)
	if a.fail != nil {
		return nil, a.fail
	}
	token := strings.TrimPrefix(s.MCPServers[0].Headers["Authorization"], "Bearer ")
	sess := &scripted{sub: a.sub, job: s.Job, token: token, turns: a.turns, hang: a.hang, limited: a.limited, stderr: []string{"still thinking"}}
	a.sessions = append(a.sessions, sess)
	gone := make(chan struct{})
	a.gone = append(a.gone, gone)
	return closable{sess, &sync.Once{}, gone}, nil
}

// cut tells whether the i-th session started was closed.
func (a *launchedAgents) cut(i int) bool {
	a.mu.Lock()
	gone := a.gone[i]
	a.mu.Unlock()
	select {
	case <-gone:
		return true
	default:
		return false
	}
}

func (a *launchedAgents) started() []ports.SessionSpec {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ports.SessionSpec{}, a.specs...)
}

// closable is a scripted session as ports.Session; gone is closed with it.
type closable struct {
	*scripted
	once *sync.Once
	gone chan struct{}
}

func (closable) Cancel() error                { return nil }
func (closable) Resume(context.Context) error { return ports.ErrResumeUnsupported }

func (c closable) Close() error {
	c.once.Do(func() {
		c.closed = true
		close(c.gone)
	})
	return nil
}

// inline runs work at once, in the caller, unless refuse is set, as a
// scheduler with a free slot or one that will not launch it. It counts the
// work handed to it.
type inline struct {
	refuse error
	mu     sync.Mutex
	n      int
}

func (s *inline) Submit(ctx context.Context, _ domain.JobID, w scheduler.Work) <-chan error {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	done := make(chan error, 1)
	if s.refuse != nil {
		done <- s.refuse
	} else {
		done <- w(ctx)
	}
	return done
}

// sessionTimeout is how long the launcher's sessions may run.
const sessionTimeout = time.Hour

// handTimer is a timer whose durations run out only when the test closes
// fire; every duration it is asked for is sent on asked.
type handTimer struct {
	fire  chan time.Time
	asked chan time.Duration
}

func newHandTimer() *handTimer {
	return &handTimer{fire: make(chan time.Time), asked: make(chan time.Duration, 8)}
}

func (t *handTimer) After(d time.Duration) <-chan time.Time {
	t.asked <- d
	return t.fire
}

// eachTimer is a timer whose every duration runs out only when the test
// closes the channel it was handed on asked for it.
type eachTimer struct{ asked chan chan time.Time }

func (t eachTimer) After(time.Duration) <-chan time.Time {
	fire := make(chan time.Time)
	t.asked <- fire
	return fire
}

// heldLocks are a job's gate locks whose first Preempt, which a submitted
// result runs once its token is authorized and while it holds its job's
// lock, signals entered and waits for release.
type heldLocks struct {
	*gatelock.Locks
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (l *heldLocks) Preempt(ctx context.Context, job domain.JobID) error {
	l.once.Do(func() {
		close(l.entered)
		<-l.release
	})
	return l.Locks.Preempt(ctx, job)
}

// lockedBuffer is a log written from the launcher's goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func reviewPipeline() domain.Pipeline {
	return domain.Pipeline{Name: "default", Stages: []domain.Stage{
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code", "reviewer-security"}, OnChanges: "build"},
	}}
}

// launcher launches sessions of jobs following p on agents through sched.
func (w world) launcher(p domain.Pipeline, agents *launchedAgents, sched *inline, log *lockedBuffer) *worker.Launcher {
	ps := pipelines{"default": p}
	return &worker.Launcher{
		Tokens: w.tokens, Jobs: w.store, Pipelines: ps, Roles: kindRoles{}, Agents: agents, Worktrees: sameDir{},
		Scheduler: sched, Attempts: &worker.Attempts{Results: w.sub, Jobs: w.store, Pipelines: ps, Clock: stopped{}},
		MCP: func(job domain.JobID) string { return "http://factoryd/" + string(job) }, Log: log,
		Timeout: sessionTimeout, Clock: newHandTimer(),
	}
}

// decided decides ev on job id following p and returns the last event of
// type T it saved.
func decided[T domain.Event](ctx *specs.Context, w world, p domain.Pipeline, id domain.JobID, ev domain.Event) T {
	_, v, err := w.store.Load(context.Background(), id)
	ctx.Expect(err).To(specs.BeNil())
	evs, _, err := w.store.Apply(context.Background(), id, v, p, ev)
	ctx.Expect(err).To(specs.BeNil())
	var last T
	found := false
	for _, e := range evs {
		if t, ok := e.(T); ok {
			last, found = t, true
		}
	}
	ctx.Expect(found).To(specs.BeTrue())
	return last
}

// enteredBuild takes job id through a failed gate back to build, in a new
// round, and returns its saved entry into build.
func enteredBuild(ctx *specs.Context, w world, id domain.JobID) domain.StageEntered {
	return enteredBuildAt(ctx, w, id, at)
}

func enteredBuildAt(ctx *specs.Context, w world, id domain.JobID, when time.Time) domain.StageEntered {
	w.apply(ctx, id, domain.ResultSubmitted{Job: id, Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: when})
	return decided[domain.StageEntered](ctx, w, pipeline(), id, domain.GateFailed{Job: id, Run: domain.ChecksRun{Command: "make check", ExitCode: 2}, At: when})
}

// eventually fails the test unless ok holds within a few seconds.
func eventually(ctx *specs.Context, ok func() bool) {
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			ctx.Testing().Fatal("it never happened")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// launch hands ev to l; inline runs the attempts before it returns.
func launch(ctx *specs.Context, l *worker.Launcher, ev domain.Event) {
	ctx.Expect(l.Launch(context.Background(), ev)).To(specs.BeNil())
}

func tokenOf(s ports.SessionSpec) string {
	return strings.TrimPrefix(s.MCPServers[0].Headers["Authorization"], "Bearer ")
}

// lastEvents names id's last n saved events.
func lastEvents(ctx *specs.Context, w world, id domain.JobID, n int) []string {
	h := w.history(ctx, id)
	return h[max(len(h)-n, 0):]
}

func TestLaunch(t *testing.T) {
	specs.Describe(t, "worker.Launcher", func(s *specs.Spec) {
		s.It("launches the stage's role with a new token for its job, role and stage entry, the job's MCP server and the role's tools", func(ctx *specs.Context) {
			w := newWorld(ctx)
			build := enteredBuild(ctx, w, "j-1")
			agents := &launchedAgents{sub: w.sub, turns: []string{ready}}

			launch(ctx, w.launcher(pipeline(), agents, &inline{}, &lockedBuffer{}), build)

			started := agents.started()
			ctx.Expect(started).To(specs.HaveLen(1))
			spec := started[0]
			ctx.Expect(spec.Job).ToEqual(domain.JobID("j-1"))
			ctx.Expect(spec.Role).ToEqual("builder")
			ctx.Expect(spec.Kind).ToEqual(domain.KindBuild)
			ctx.Expect(spec.Dir).ToEqual("/wt/j-1")
			ctx.Expect(spec.MCPServers).To(specs.HaveLen(1))
			ctx.Expect(spec.MCPServers[0].Name).ToEqual(worker.MCPServerName)
			ctx.Expect(spec.MCPServers[0].URL).ToEqual("http://factoryd/j-1")
			ctx.Expect(spec.Tools).ToEqual(builderTools)
			ctx.Expect(tokenOf(spec)).To(specs.Not(specs.Equal(w.j1)))
			sess := agents.sessions[0]
			ctx.Expect(sess.prompts).ToEqual([]string{"prompt of builder"})
			// The token was for round 2's entry into build: the result was saved.
			ctx.Expect(sess.answers).ToEqual([]error{nil})
			ctx.Expect(sess.closed).To(specs.BeTrue())
			job, _, err := w.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual(domain.StageGate)
			_, err = w.tokens.Authorize(tokenOf(spec), jobtoken.OpSubmitResult, "j-1")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
		})

		s.It("revokes the token of a session that ends without a result", func(ctx *specs.Context) {
			w := newWorld(ctx)
			build := enteredBuild(ctx, w, "j-1")
			agents := &launchedAgents{sub: w.sub}

			launch(ctx, w.launcher(pipeline(), agents, &inline{}, &lockedBuffer{}), build)

			ctx.Expect(lastEvents(ctx, w, "j-1", 2)).ToEqual([]string{"AttemptFailed", "AttemptRetried"})
			ctx.Expect(agents.sessions[0].closed).To(specs.BeTrue())
			_, err := w.tokens.Authorize(tokenOf(agents.started()[0]), jobtoken.OpProgress, "j-1")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
		})

		s.It("relaunches a retried role in a new session with another token", func(ctx *specs.Context) {
			w := newWorld(ctx)
			build := enteredBuild(ctx, w, "j-1")
			agents := &launchedAgents{sub: w.sub}
			l := w.launcher(pipeline(), agents, &inline{}, &lockedBuffer{})
			launch(ctx, l, build)
			hist, err := w.store.History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			retried, ok := hist[len(hist)-1].Event.(domain.AttemptRetried)
			ctx.Expect(ok).To(specs.BeTrue())
			agents.turns = []string{ready}

			launch(ctx, l, retried)

			started := agents.started()
			ctx.Expect(started).To(specs.HaveLen(2))
			ctx.Expect(started[1].Role).ToEqual("builder")
			ctx.Expect(tokenOf(started[1])).To(specs.Not(specs.Equal(tokenOf(started[0]))))
			ctx.Expect(lastEvents(ctx, w, "j-1", 3)).ToEqual([]string{"AttemptFailed", "AttemptRetried", "StageEntered"})
		})

		s.It("hands an event the bus delivers again to the scheduler only once", func(ctx *specs.Context) {
			w := newWorld(ctx)
			build := enteredBuild(ctx, w, "j-1")
			// A scheduler that launches nothing leaves the job as it is.
			sched := &inline{refuse: errors.New("full")}
			l := w.launcher(pipeline(), &launchedAgents{sub: w.sub}, sched, &lockedBuffer{})

			launch(ctx, l, build)
			launch(ctx, l, build)

			ctx.Expect(sched.n).ToEqual(1)
		})

		s.It("launches nothing for a stage entry the job left, an event it never saved or a stage without agents", func(ctx *specs.Context) {
			w := newWorld(ctx)
			agents := &launchedAgents{sub: w.sub}
			sched := &inline{}
			l := w.launcher(pipeline(), agents, sched, &lockedBuffer{})
			first := enteredBuild(ctx, w, "j-1")
			// Round 3 enters build again: round 2's entry is stale.
			enteredBuildAt(ctx, w, "j-1", at.Add(time.Hour))
			w.apply(ctx, "j-2", domain.ResultSubmitted{Job: "j-2", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})

			launch(ctx, l, first)
			launch(ctx, l, domain.AttemptRetried{Job: "j-1", Role: "builder", Retry: 1, At: at.Add(time.Hour)})
			launch(ctx, l, domain.StageEntered{Job: "j-2", Stage: domain.StageGate, At: at})

			ctx.Expect(sched.n).ToEqual(0)
			ctx.Expect(agents.started()).To(specs.BeEmpty())
		})

		s.It("launches every reviewer of a review stage, even after one's attempt failed", func(ctx *specs.Context) {
			w := newWorld(ctx)
			p := reviewPipeline()
			w.apply(ctx, "j-1", domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})
			review := decided[domain.StageEntered](ctx, w, p, "j-1", domain.GatePassed{Job: "j-1", Run: domain.ChecksRun{Command: "make check"}, At: at})
			agents := &launchedAgents{sub: w.sub}

			launch(ctx, w.launcher(p, agents, &inline{}, &lockedBuffer{}), review)

			var roles []string
			for _, s := range agents.started() {
				roles = append(roles, s.Role)
			}
			ctx.Expect(roles).ToEqual([]string{"reviewer-code", "reviewer-security"})
		})

		s.It("launches no session for a reviewer whose when does not apply to the diff", func(ctx *specs.Context) {
			w := newWorld(ctx)
			p := reviewPipeline()
			p.Roles = []domain.Role{{Name: "reviewer-security", Kind: domain.KindReview, Mode: domain.ModeBlocking, When: domain.When{Any: []domain.Condition{{Paths: []string{"auth/**"}}}}}}
			w.apply(ctx, "j-1", domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})
			gate := domain.GatePassed{Job: "j-1", Run: domain.ChecksRun{Command: "make check"}, Diff: &domain.DiffSummary{Paths: []string{"a.go"}, Lines: 1}, At: at}
			review := decided[domain.StageEntered](ctx, w, p, "j-1", gate)
			agents := &launchedAgents{sub: w.sub}

			launch(ctx, w.launcher(p, agents, &inline{}, &lockedBuffer{}), review)

			var roles []string
			for _, s := range agents.started() {
				roles = append(roles, s.Role)
			}
			ctx.Expect(roles).ToEqual([]string{"reviewer-code"})
		})

		s.It("records an agent that does not start as a failed attempt", func(ctx *specs.Context) {
			w := newWorld(ctx)
			build := enteredBuild(ctx, w, "j-1")
			agents := &launchedAgents{sub: w.sub, fail: errors.New("no such command")}

			launch(ctx, w.launcher(pipeline(), agents, &inline{}, &lockedBuffer{}), build)

			ctx.Expect(w.failed(ctx).Reason).ToEqual("the agent did not start: no such command")
		})

		s.It("starts no session and logs why when the scheduler does not launch it", func(ctx *specs.Context) {
			w := newWorld(ctx)
			build := enteredBuild(ctx, w, "j-1")
			before := w.history(ctx, "j-1")
			agents := &launchedAgents{sub: w.sub}
			log := &lockedBuffer{}

			launch(ctx, w.launcher(pipeline(), agents, &inline{refuse: errors.New("no cost source configured")}, log), build)

			eventually(ctx, func() bool { return log.String() != "" })
			ctx.Expect(log.String()).ToEqual("launch: job j-1, role builder: no cost source configured\n")
			ctx.Expect(agents.started()).To(specs.BeEmpty())
			ctx.Expect(w.history(ctx, "j-1")).ToEqual(before)
		})

		s.It("stops the job in needs-human once when the scheduler did not launch its reviewers for its spent budget", func(ctx *specs.Context) {
			w := newWorld(ctx)
			p := reviewPipeline()
			w.apply(ctx, "j-1", domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})
			review := decided[domain.StageEntered](ctx, w, p, "j-1", domain.GatePassed{Job: "j-1", Run: domain.ChecksRun{Command: "make check"}, At: at})
			agents := &launchedAgents{sub: w.sub}
			log := &lockedBuffer{}
			spent := &scheduler.ExhaustedError{Job: "j-1", Spent: 5, Budget: 5}

			launch(ctx, w.launcher(p, agents, &inline{refuse: spent}, log), review)

			eventually(ctx, func() bool { return strings.Count(log.String(), "\n") == 2 })
			ctx.Expect(agents.started()).To(specs.BeEmpty())
			job, _, err := w.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Stage).ToEqual(domain.StageNeedsHuman)
			ctx.Expect(job.Cause).ToEqual(domain.CauseBudget)
			ctx.Expect(job.Paused).ToEqual(&domain.Paused{Stage: "review", Spent: 5})
			ctx.Expect(lastEvents(ctx, w, "j-1", 2)).ToEqual([]string{"StageEntered", "NeedsHuman"})
		})
	})
}

func TestLaunchTimeout(t *testing.T) {
	specs.Describe(t, "worker.Launcher's session time limit", func(s *specs.Spec) {
		s.It("cuts a session that runs past its time limit and records it as a failed attempt, so it is retried", func(ctx *specs.Context) {
			w := newWorld(ctx)
			build := enteredBuild(ctx, w, "j-1")
			agents := &launchedAgents{sub: w.sub, hang: true}
			l := w.launcher(pipeline(), agents, &inline{}, &lockedBuffer{})
			timer := newHandTimer()
			l.Clock = timer
			done := make(chan struct{})
			go func() {
				defer close(done)
				launch(ctx, l, build)
			}()

			ctx.Expect(<-timer.asked).ToEqual(sessionTimeout)
			eventually(ctx, func() bool { return len(agents.started()) == 1 })
			ctx.Expect(lastEvents(ctx, w, "j-1", 1)).ToEqual([]string{"StageEntered"})
			close(timer.fire)
			<-done

			ctx.Expect(lastEvents(ctx, w, "j-1", 2)).ToEqual([]string{"AttemptFailed", "AttemptRetried"})
			failed := w.failed(ctx)
			ctx.Expect(failed.Reason).ToEqual("the session ran past its time limit of 1h0m0s and was cut")
			ctx.Expect(failed.Stderr).ToEqual([]string{"still thinking"})
			sess := agents.sessions[0]
			// Cut, not asked again: the retry runs in a new session.
			ctx.Expect(sess.prompts).ToEqual([]string{"prompt of builder"})
			ctx.Expect(sess.closed).To(specs.BeTrue())
			_, err := w.tokens.Authorize(tokenOf(agents.started()[0]), jobtoken.OpProgress, "j-1")
			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrUnknownToken))
		})

		s.It("refuses the session's token at the cut, and records no failure when a result submitted before it is saved after", func(ctx *specs.Context) {
			w := newWorld(ctx)
			p := reviewPipeline()
			w.apply(ctx, "j-1", domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at})
			review := decided[domain.StageEntered](ctx, w, p, "j-1", domain.GatePassed{Job: "j-1", Run: domain.ChecksRun{Command: "make check"}, At: at})
			locks := &heldLocks{Locks: gatelock.New(), entered: make(chan struct{}), release: make(chan struct{})}
			w.sub.Pipelines = pipelines{"default": p}
			w.sub.Locks = locks
			agents := &launchedAgents{sub: w.sub, hang: true}
			l := w.launcher(p, agents, &inline{}, &lockedBuffer{})
			timer := eachTimer{asked: make(chan chan time.Time, 2)}
			l.Clock = timer
			done := make(chan struct{})
			go func() {
				defer close(done)
				launch(ctx, l, review)
			}()

			// reviewer-code's session hands in its result, which waits on the
			// job's lock until the test releases it.
			codeLimit := <-timer.asked
			eventually(ctx, func() bool { return len(agents.started()) == 1 })
			token := tokenOf(agents.started()[0])
			submitted := make(chan error, 1)
			go func() {
				_, err := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"verdict":"PASS"}`))
				submitted <- err
			}()
			<-locks.entered

			close(codeLimit)
			// The cut takes the token away at once, while the attempt still waits
			// to record its failure.
			eventually(ctx, func() bool {
				_, err := w.tokens.Authorize(token, jobtoken.OpSubmitResult, "j-1")
				return errors.Is(err, jobtoken.ErrUnknownToken)
			})
			eventually(ctx, func() bool { return agents.cut(0) })
			select {
			case <-done:
				ctx.Testing().Fatal("the launch ended before the result was in")
			default:
			}
			close(locks.release)
			ctx.Expect(<-submitted).To(specs.BeNil())
			// reviewer-security's session runs past its limit with no result.
			close(<-timer.asked)
			<-done

			job, _, err := w.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(job.Passed).ToEqual([]string{"reviewer-code"})
			var failures []string
			hist, err := w.store.History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			for _, h := range hist {
				if e, ok := h.Event.(domain.AttemptFailed); ok {
					failures = append(failures, e.Role)
				}
			}
			ctx.Expect(failures).ToEqual([]string{"reviewer-security"})
			// The cut session's outcome was taken, so it is not kept for ever.
			ctx.Expect(w.sub.Take(token)).ToEqual(worker.Outcome{})
		})
	})
}
