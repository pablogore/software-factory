package wiring_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/queue"
	"github.com/pablogore/software-factory/internal/platform/runmode"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
)

// issueJobs is a fake runmode.Reports: the jobs of each issue and what they
// cost, kept across restarts as factoryd's store and cost record would be.
type issueJobs struct {
	mu     sync.Mutex
	issues map[int][]float64
}

// set makes issue n's jobs one slice per cost in usd, "<n>-s1" onwards.
func (r *issueJobs) set(n int, usd ...float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.issues == nil {
		r.issues = map[int][]float64{}
	}
	r.issues[n] = usd
}

func (r *issueJobs) Report(_ context.Context, job domain.JobID) (runmode.IssueReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n, s int
	if _, err := fmt.Sscanf(string(job), "%d-s%d", &n, &s); err != nil || s < 1 || s > len(r.issues[n]) {
		return runmode.IssueReport{}, fmt.Errorf("job %q: %w", job, ports.ErrJobNotFound)
	}
	origin := domain.Origin{Provider: "github", Thread: fmt.Sprintf("issue/%d", n), Author: "ana"}
	rep := runmode.IssueReport{Issue: queue.Issue{Number: n, Title: fmt.Sprintf("Issue %d", n)}}
	for i, usd := range r.issues[n] {
		id := domain.JobID(fmt.Sprintf("%d-s%d", n, i+1))
		rep.Jobs = append(rep.Jobs, runmode.JobReport{Job: domain.Job{ID: id, Stage: domain.StageDone, Initiative: domain.InitiativeID(fmt.Sprint(n)), Origin: origin}, USD: usd})
	}
	return rep, nil
}

// capStops is a fake runmode.Outbox and runmode.StopInbox: the stops one life
// of factoryd published.
type capStops struct {
	mu    sync.Mutex
	stops []runmode.Stop
}

func (*capStops) Enqueue(context.Context, domain.JobID, ...ports.Action) error { return nil }

func (c *capStops) PutStop(_ context.Context, _ string, s runmode.Stop) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops = append(c.stops, s)
	return nil
}

func (c *capStops) last() runmode.Stop {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.stops) == 0 {
		return runmode.Stop{}
	}
	return c.stops[len(c.stops)-1]
}

// slicesRun is a fake runmode.Slicer: it keeps the slices that ran, across
// restarts.
type slicesRun struct {
	mu  sync.Mutex
	ran []domain.JobID
}

func (r *slicesRun) Work(_ context.Context, job domain.JobID) (scheduler.Work, error) {
	return func(context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.ran = append(r.ran, job)
		return nil
	}, nil
}

func (r *slicesRun) all() []domain.JobID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ran)
}

// capRun is one life of factoryd's caps on the store at path, as a restart
// builds it again.
type capRun struct {
	store   *sqlite.Store
	starter *runmode.Starter
	stops   *capStops
}

func startCaps(ctx *specs.Context, path string, reps *issueJobs, ran *slicesRun) capRun {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	sched, err := scheduler.New(1, 5, noCosts{}, scheduler.GitHub{Limits: plentyLimit{}, Threshold: 100, Clock: stillClock{}})
	ctx.Expect(err).To(specs.BeNil())
	c := capRun{store: st, stops: &capStops{}}
	c.starter, err = runmode.NewStarter(sched, ran, runmode.DefaultCaps, reps, c.stops, c.stops, st)
	ctx.Expect(err).To(specs.BeNil())
	return c
}

func (c capRun) start(bg context.Context, after, job domain.JobID) (<-chan error, error) {
	return c.starter.Start(bg, domain.NextSliceRequested{Job: after, Initiative: "1", At: brakeEpoch}, job)
}

func (c capRun) stop(ctx *specs.Context) {
	ctx.Expect(c.store.Close()).To(specs.BeNil())
}

// raise is an allowed person's raise of issue job's cap to value, answering
// wait seq.
func raise(job domain.JobID, c domain.IssueCap, value float64, seq int) domain.CapRaiseRequested {
	return domain.CapRaiseRequested{Job: job, By: "pablogore", AuthorKind: domain.AuthorPerson, Allowed: true, Cap: c, Value: value, WaitSeq: seq, At: brakeEpoch}
}

func awaitSlice(ctx *specs.Context, done <-chan error) {
	select {
	case err := <-done:
		ctx.Expect(err).To(specs.BeNil())
	case <-time.After(brakePatience):
		ctx.Errorf("the slice did not finish")
	}
}

func TestCapStopSurvivesRestart(t *testing.T) {
	specs.Describe(t, "an issue stopped at a cap across a restart of factoryd", func(s *specs.Spec) {
		bg := context.Background()

		s.It("stays stopped under its wait after a restart, and starts its next slice once a person raises that cap", func(ctx *specs.Context) {
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			reps, ran := &issueJobs{}, &slicesRun{}
			// Five slices of USD 2: past both caps, so a raise of the
			// slices stops it again at USD 8, under wait 2.
			reps.set(1, 2, 2, 2, 2, 2)

			first := startCaps(ctx, path, reps, ran)
			_, err := first.start(bg, "1-s5", "1-s6")
			ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
			ctx.Expect(first.stops.last().WaitSeq).ToEqual(1)
			_, err = first.starter.Raise(bg, raise("1-s5", domain.CapSlices, 6, 1))
			ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
			ctx.Expect(first.stops.last()).ToEqual(runmode.Stop{Issue: 1, Title: "Issue 1", Cap: domain.CapUSD, Limit: 8, Reached: 10, WaitSeq: 2})
			first.stop(ctx)

			second := startCaps(ctx, path, reps, ran)
			ctx.Cleanup(func() { _ = second.store.Close() })
			// The slice is handed over again after the restart: it does
			// not start, and the stop keeps its wait and its cap, since the
			// slices raised before the restart stay raised.
			_, err = second.start(bg, "1-s5", "1-s6")
			ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
			ctx.Expect(second.stops.last()).ToEqual(runmode.Stop{Issue: 1, Title: "Issue 1", Cap: domain.CapUSD, Limit: 8, Reached: 10, WaitSeq: 2})
			_, err = second.starter.Raise(bg, raise("1-s5", domain.CapUSD, 12, 1))
			code, _ := domain.RaiseRefusalOf(err)
			ctx.Expect(code).ToEqual(domain.RaiseStaleWait)
			ctx.Expect(ran.all()).To(specs.BeEmpty())

			done, err := second.starter.Raise(bg, raise("1-s5", domain.CapUSD, 12, 2))
			ctx.Expect(err).To(specs.BeNil())
			awaitSlice(ctx, done)
			ctx.Expect(ran.all()).ToEqual([]domain.JobID{"1-s6"})
		})

		s.It("accepts a raise after a restart without the slice handed over again", func(ctx *specs.Context) {
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			reps, ran := &issueJobs{}, &slicesRun{}
			reps.set(1, 1, 1, 1, 1, 1)

			first := startCaps(ctx, path, reps, ran)
			_, err := first.start(bg, "1-s5", "1-s6")
			ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
			first.stop(ctx)

			second := startCaps(ctx, path, reps, ran)
			ctx.Cleanup(func() { _ = second.store.Close() })
			done, err := second.starter.Raise(bg, raise("1-s5", domain.CapSlices, 6, 1))
			ctx.Expect(err).To(specs.BeNil())
			awaitSlice(ctx, done)
			ctx.Expect(ran.all()).ToEqual([]domain.JobID{"1-s6"})
		})

		s.It("keeps a cap raised before a restart raised after it", func(ctx *specs.Context) {
			path := filepath.Join(ctx.Testing().TempDir(), "factory.db")
			reps, ran := &issueJobs{}, &slicesRun{}
			reps.set(1, 1, 1, 1, 1, 1)

			first := startCaps(ctx, path, reps, ran)
			_, err := first.start(bg, "1-s5", "1-s6")
			ctx.Expect(errors.Is(err, runmode.ErrCapped)).To(specs.BeTrue())
			done, err := first.starter.Raise(bg, raise("1-s5", domain.CapSlices, 7, 1))
			ctx.Expect(err).To(specs.BeNil())
			awaitSlice(ctx, done)
			first.stop(ctx)

			reps.set(1, 1, 1, 1, 1, 1, 1)
			second := startCaps(ctx, path, reps, ran)
			ctx.Cleanup(func() { _ = second.store.Close() })
			done, err = second.start(bg, "1-s6", "1-s7")
			ctx.Expect(err).To(specs.BeNil())
			awaitSlice(ctx, done)
			ctx.Expect(ran.all()).ToEqual([]domain.JobID{"1-s6", "1-s7"})
		})
	})
}
