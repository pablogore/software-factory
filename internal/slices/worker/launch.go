package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
)

// LaunchEvents are the events a Launcher launches role sessions on: a job
// entering a stage, and a failed attempt that runs again.
var LaunchEvents = []string{domain.StageEntered{}.EventName(), domain.AttemptRetried{}.EventName()}

// MCPServerName is the name the job's worker MCP server goes by in a
// session.
const MCPServerName = "factoryd"

// Launcher runs a role's attempt at a job in a new agent session each time
// the job enters a pipeline stage, for every role that runs in it, and each
// time a failed attempt runs again, for that role. Every session gets a new
// token for its job, role and stage entry, and the job's MCP server with it;
// the token is revoked when the session ends. A session still running
// Timeout after it started is cut, its token revoked and its agent stopped
// at once, and its attempt fails, so a hung agent does not hold its slot
// for ever. The attempts run through Scheduler, so it
// decides whether and when each launches; an attempt it never launched for
// the job's spent budget stops the job in needs-human.
type Launcher struct {
	Tokens    issuer
	Jobs      jobs
	Pipelines pipelines
	Roles     roles
	Agents    agents
	Worktrees worktrees
	Scheduler slots
	Attempts  *Attempts
	// MCP is the URL of job's worker MCP server.
	MCP func(job domain.JobID) string
	// Log is where an attempt that could not run or failed to record is
	// written, since no one waits for it.
	Log io.Writer
	// Timeout is how long a session may run, measured on Clock.
	Timeout time.Duration
	Clock   timer

	// mu guards launched and Log.
	mu sync.Mutex
	// launched keeps the attempts already handed to Scheduler, since the bus
	// may hand an event over again.
	launched map[launchKey]bool
	// stopping records one spent budget at a time, so the roles of one
	// stage never race to stop their job.
	stopping sync.Mutex
}

// launchKey is one attempt of a role: the job's round and stage entry, and
// the retry it is, 0 for the first.
type launchKey struct {
	job   domain.JobID
	role  string
	round int
	entry int
	retry int
}

// Launch hands Scheduler an attempt for each role ev starts: every role that
// runs in the stage a StageEntered names, or the role an AttemptRetried
// names. It never waits for them. An event saved in a stage entry the job
// has since left launches nothing, nor does one for a stage outside the
// pipeline, such as the gate, where no agent runs. ctx is the attempts'
// context too.
func (l *Launcher) Launch(ctx context.Context, ev domain.Event) error {
	job, _, err := l.Jobs.Load(ctx, ev.JobID())
	if err != nil {
		return fmt.Errorf("worker: launch: %w", err)
	}
	p, err := l.Pipelines.Pipeline(ctx, job.Pipeline)
	if err != nil {
		return fmt.Errorf("worker: launch: %w", err)
	}
	i := slices.IndexFunc(p.Stages, func(s domain.Stage) bool { return s.Name == job.Stage })
	if i < 0 {
		return nil
	}
	hist, err := l.Jobs.History(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("worker: launch: %w", err)
	}
	entry := jobtoken.Entry(hist)
	if savedIn(hist, ev) != entry {
		return nil
	}
	running := runningRoles(job, p.Stages[i])
	launch, retry := running, 0
	if e, ok := ev.(domain.AttemptRetried); ok {
		if !slices.Contains(running, e.Role) {
			return nil
		}
		launch, retry = []string{e.Role}, e.Retry
	}
	for _, role := range launch {
		key := launchKey{job: job.ID, role: role, round: job.Round, entry: entry, retry: retry}
		if !l.claim(key) {
			continue
		}
		done := l.Scheduler.Submit(ctx, job.ID, func(ctx context.Context) error { return l.attempt(ctx, key) })
		go l.report(ctx, key, done)
	}
	return nil
}

// savedIn is the stage entry the job was in once ev was saved, from its
// history hist, or -1 when ev is not in it. The last record like ev counts.
func savedIn(hist []ports.Record, ev domain.Event) int {
	for i := len(hist) - 1; i >= 0; i-- {
		if same(hist[i].Event, ev) {
			return jobtoken.Entry(hist[:i+1])
		}
	}
	return -1
}

// same tells whether a and b are the same StageEntered or AttemptRetried. A
// saved time may come back in another location, so times are compared as
// instants.
func same(a, b domain.Event) bool {
	switch x := a.(type) {
	case domain.StageEntered:
		y, ok := b.(domain.StageEntered)
		return ok && x.Job == y.Job && x.Stage == y.Stage && x.At.Equal(y.At)
	case domain.AttemptRetried:
		y, ok := b.(domain.AttemptRetried)
		return ok && x.Job == y.Job && x.Role == y.Role && x.Retry == y.Retry && x.At.Equal(y.At)
	}
	return false
}

// runningRoles are the roles that run in stage st this round: every role of
// the stage, or only job.Reviewers when a later review round names them, as
// decide takes their results.
func runningRoles(job domain.Job, st domain.Stage) []string {
	if st.Kind == domain.KindReview && len(job.Reviewers) > 0 {
		return job.Reviewers
	}
	return st.Roles
}

// claim tells whether key was not launched yet, and marks it launched.
func (l *Launcher) claim(key launchKey) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.launched[key] {
		return false
	}
	if l.launched == nil {
		l.launched = map[launchKey]bool{}
	}
	l.launched[key] = true
	return true
}

// attempt runs key's attempt in a new session, unless the job left its round
// or stage entry while it waited for a slot. A role, worktree or agent that fails to start the
// session is a failed attempt, so the role runs again or the job hits a
// wall instead of waiting for a session that never came.
func (l *Launcher) attempt(ctx context.Context, key launchKey) error {
	job, _, err := l.Jobs.Load(ctx, key.job)
	if err != nil {
		return err
	}
	hist, err := l.Jobs.History(ctx, key.job)
	if err != nil {
		return err
	}
	if job.Round != key.round || jobtoken.Entry(hist) != key.entry {
		return nil
	}
	role, prompt, err := l.Roles.Role(ctx, key.role)
	if err != nil {
		return l.Attempts.fail(ctx, key.job, key.role, "the role's session could not start: "+err.Error(), nil, nil)
	}
	token, err := l.Tokens.Issue(jobtoken.For(job, hist, role))
	if err != nil {
		return err
	}
	defer l.Tokens.Revoke(token)
	wt, err := l.Worktrees.Prepare(ctx, key.job)
	if err != nil {
		return l.Attempts.fail(ctx, key.job, key.role, "the job's worktree could not be prepared: "+err.Error(), nil, nil)
	}
	mcp := ports.MCPServer{Name: MCPServerName, URL: l.MCP(key.job), Headers: map[string]string{"Authorization": "Bearer " + token}}
	sess, err := l.Agents.Start(ctx, role, ports.SessionSpec{Job: key.job, Role: role.Name, Kind: role.Kind, Dir: wt.Path, MCPServers: []ports.MCPServer{mcp}})
	if err != nil {
		return l.Attempts.fail(ctx, key.job, key.role, "the agent did not start: "+err.Error(), nil, nil)
	}
	defer func() { _ = sess.Close() }()
	run, stop, expired := l.deadline(ctx, func() {
		l.Tokens.Revoke(token)
		_ = sess.Close()
	})
	defer stop()
	_, err = l.Attempts.Run(run, sess, key.job, role.Name, token, prompt)
	stop()
	if err == nil || !expired() {
		return err
	}
	return l.timedOut(ctx, key, token, sess.Stderr())
}

// deadline is the context a session runs with, which ends with ctx or once
// the session ran past Timeout, and tells whether Timeout ran out. Once it
// runs out, the context ends and cut runs at once, without waiting for the
// session to notice. stop ends the context and returns once cut, if it
// started, is done; it may be called again.
func (l *Launcher) deadline(ctx context.Context, cut func()) (run context.Context, stop func(), expired func() bool) {
	run, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	timedOut := false
	after := l.Clock.After(l.Timeout)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-after:
			mu.Lock()
			timedOut = true
			mu.Unlock()
			cancel()
			cut()
		case <-run.Done():
		}
	}()
	stop = func() {
		cancel()
		<-exited
	}
	return run, stop, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return timedOut
	}
}

// timedOut records key's attempt as failed for running past Timeout, with
// the session's last stderr lines, unless a result or question was saved
// with token, the session's token, or the job left key's round or stage
// entry: both as they stand once every submission with token is in.
func (l *Launcher) timedOut(ctx context.Context, key launchKey, token string, stderr []string) error {
	reason := fmt.Sprintf("the session ran past its time limit of %s and was cut", l.Timeout)
	_, err := l.Attempts.failSession(ctx, key.job, key.role, token, reason, stderr, func(job domain.Job, hist []ports.Record) bool {
		return job.Round == key.round && jobtoken.Entry(hist) == key.entry
	})
	return err
}

// report writes to Log why key's attempt did not run or did not finish. An
// attempt that never ran for the job's spent budget records it first.
func (l *Launcher) report(ctx context.Context, key launchKey, done <-chan error) {
	err := <-done
	var ex *scheduler.ExhaustedError
	if errors.As(err, &ex) {
		err = errors.Join(err, l.exhausted(ctx, key, ex.Spent))
	}
	if err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.Log, "launch: job %s, role %s: %v\n", key.job, key.role, err)
}

// exhausted records that key's job had spent its budget when key's attempt
// was about to launch, unless the job left key's round or stage entry, as
// another role of the same stage stopping it first does.
func (l *Launcher) exhausted(ctx context.Context, key launchKey, spent float64) error {
	l.stopping.Lock()
	defer l.stopping.Unlock()
	ev := domain.BudgetExhausted{Job: key.job, Spent: spent, At: l.Attempts.Clock.Now()}
	err := l.Attempts.record(ctx, key.job, ev, func(job domain.Job, hist []ports.Record) bool {
		return job.Round == key.round && jobtoken.Entry(hist) == key.entry
	})
	if err != nil {
		return fmt.Errorf("worker: budget exhausted: %w", err)
	}
	return nil
}
