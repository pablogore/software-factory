package wiring_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/childenv"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/slices/worker"
	"github.com/pablogore/software-factory/internal/wiring"
)

var serveEpoch = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// tickClock is a fake scheduler.Clock: time moves only on Advance, and
// every After it is asked for is sent on asked, so a test knows the server
// is waiting before it moves time.
type tickClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []tickWaiter
	asked   chan time.Duration
}

type tickWaiter struct {
	at time.Time
	c  chan time.Time
}

func newTickClock() *tickClock {
	return &tickClock{now: serveEpoch, asked: make(chan time.Duration, 64)}
}

func (c *tickClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *tickClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, tickWaiter{at: c.now.Add(d), c: ch})
	c.mu.Unlock()
	c.asked <- d
	return ch
}

// Advance moves time by d and wakes every waiter now due.
func (c *tickClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			kept = append(kept, w)
			continue
		}
		w.c <- c.now
	}
	c.waiters = kept
}

// waitAsked is the next wait the server asks the clock for. It fails the
// test rather than hang when none comes.
func waitAsked(ctx *specs.Context, c *tickClock) time.Duration {
	select {
	case d := <-c.asked:
		return d
	case <-time.After(10 * time.Second):
		ctx.Testing().Fatal("the server never waited on the clock")
		return 0
	}
}

// destination is a fake ports.Destination. Deliver records what it was
// handed; while hold is set it first waits for release or its context.
type destination struct {
	mu        sync.Mutex
	delivered []string
	hold      bool
	called    chan struct{}
	release   chan struct{}
}

func newDestination(hold bool) *destination {
	return &destination{hold: hold, called: make(chan struct{}, 8), release: make(chan struct{})}
}

func (d *destination) Deliver(ctx context.Context, a ports.Action) error {
	d.called <- struct{}{}
	if d.hold {
		select {
		case <-d.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.delivered = append(d.delivered, a.Key)
	return nil
}

func (d *destination) Done(context.Context, string) (bool, error) { return false, nil }

func (d *destination) keys() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.delivered...)
}

const serveTick = time.Second
const serveGrace = 30 * time.Second

// serveConfig is a valid configuration over the repo's own roles and
// pipelines, with a store and dev server records in the test's own
// directory.
func serveConfig(ctx *specs.Context, clk *tickClock, dests map[string]ports.Destination) wiring.ServeConfig {
	dir := ctx.Testing().TempDir()
	return wiring.ServeConfig{
		Config: "../..", Store: filepath.Join(dir, "factoryd.db"), Repo: dir, Base: "main",
		DevState: filepath.Join(dir, "devservers"), Concurrency: 1, Budget: 5, Listen: "127.0.0.1:0",
		Agents: wiring.ACPAgents(childenv.Homes{Root: filepath.Join(dir, "homes")}, clk), Tick: serveTick, Grace: serveGrace, Clock: clk, Destinations: dests, Log: io.Discard,
	}
}

// seedJob saves job j-1 in path with one comment and one push action, then
// has its builder hand in a ready build, so the store has events pending for
// the dev server subscriber.
func seedJob(ctx *specs.Context, path string) {
	bg := context.Background()
	st, err := sqlite.Open(bg, path)
	ctx.Expect(err).To(specs.BeNil())
	defer func() { ctx.Expect(st.Close()).To(specs.BeNil()) }()
	job := domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "build", Round: 1}
	acts := []ports.Action{{Key: "j-1/comment", Kind: "comment", Payload: "hi"}, {Key: "j-1/push", Kind: "push", Payload: "branch"}}
	ctx.Expect(st.Create(bg, job, acts...)).To(specs.BeNil())
	p, err := onePipeline{}.Pipeline(bg, "default")
	ctx.Expect(err).To(specs.BeNil())
	_, v, err := st.Load(bg, "j-1")
	ctx.Expect(err).To(specs.BeNil())
	_, _, err = st.Apply(bg, "j-1", v, p, domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: serveEpoch})
	ctx.Expect(err).To(specs.BeNil())
}

// reopen is a second handle on the store at path, closed with the test.
func reopen(ctx *specs.Context, path string) *sqlite.Store {
	st, err := sqlite.Open(context.Background(), path)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Testing().Cleanup(func() { _ = st.Close() })
	return st
}

func pending(ctx *specs.Context, st *sqlite.Store) []ports.Saved {
	p, err := st.Pending(context.Background(), wiring.DevServerSubscriber, worker.DevServerEvents)
	ctx.Expect(err).To(specs.BeNil())
	return p
}

func actions(ctx *specs.Context, st *sqlite.Store) map[string]ports.QueuedAction {
	qs, err := st.Actions(context.Background(), "j-1")
	ctx.Expect(err).To(specs.BeNil())
	byKey := map[string]ports.QueuedAction{}
	for _, q := range qs {
		byKey[q.Key] = q
	}
	return byKey
}

// running is a server started from cfg and running until stop; done gets
// Run's error.
func running(ctx *specs.Context, cfg wiring.ServeConfig) (stop context.CancelFunc, done chan error) {
	srv, err := wiring.StartServer(context.Background(), cfg)
	ctx.Expect(err).To(specs.BeNil())
	run, stop := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- srv.Run(run) }()
	return stop, done
}

type failRow struct {
	piece string
	why   string
	cfg   func(ctx *specs.Context, cfg *wiring.ServeConfig)
	is    error
}

func TestServe(t *testing.T) {
	specs.Describe(t, "wiring.StartServer and Server.Run", func(s *specs.Spec) {
		s.It("starts with a valid configuration, handing the subscribers what was pending first", func(ctx *specs.Context) {
			clk := newTickClock()
			cfg := serveConfig(ctx, clk, nil)
			seedJob(ctx, cfg.Store)
			ctx.Expect(pending(ctx, reopen(ctx, cfg.Store))).To(specs.Not(specs.BeEmpty()))

			stop, done := running(ctx, cfg)

			ctx.Expect(pending(ctx, reopen(ctx, cfg.Store))).To(specs.BeEmpty())
			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		fails := []failRow{
			{"config", "a tick of 0", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Tick = 0 }, domain.ErrInvalidValue},
			{"config", "no agents", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Agents = nil }, domain.ErrInvalidValue},
			{"config", "no address to listen on", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Listen = "" }, domain.ErrInvalidValue},
			{"pipeline", "a root with no roles", func(ctx *specs.Context, cfg *wiring.ServeConfig) { cfg.Config = ctx.Testing().TempDir() }, wiring.ErrNoRoles},
			{"pipeline", "roles but no pipelines", func(ctx *specs.Context, cfg *wiring.ServeConfig) {
				// The repo's roles, without its pipelines.
				dir := ctx.Testing().TempDir()
				ctx.Expect(os.CopyFS(filepath.Join(dir, "roles"), os.DirFS("../../roles"))).To(specs.BeNil())
				agents, err := os.ReadFile("../../roles.yaml")
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(os.WriteFile(filepath.Join(dir, "roles.yaml"), agents, 0o600)).To(specs.BeNil())
				cfg.Config = dir
			}, wiring.ErrNoPipelines},
			{"store", "no store file", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Store = "" }, domain.ErrMissingField},
			{"store", "a store in a missing directory", func(ctx *specs.Context, cfg *wiring.ServeConfig) {
				cfg.Store = filepath.Join(ctx.Testing().TempDir(), "no-such-dir", "factoryd.db")
			}, nil},
			{"scheduler", "a budget of 0", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Budget = 0 }, nil},
			{"scheduler", "a concurrency of 0", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Concurrency = 0 }, nil},
			{"scheduler", "an unknown cost source", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Costs = "metered" }, wiring.ErrUnknownCostSource},
			{"worker api", "an address it cannot listen on", func(_ *specs.Context, cfg *wiring.ServeConfig) { cfg.Listen = "127.0.0.1:not-a-port" }, nil},
			{"dev servers", "dev server records under a file", func(ctx *specs.Context, cfg *wiring.ServeConfig) {
				file := filepath.Join(ctx.Testing().TempDir(), "a-file")
				ctx.Expect(os.WriteFile(file, nil, 0o600)).To(specs.BeNil())
				cfg.DevState = filepath.Join(file, "devservers")
			}, nil},
		}
		specs.Table(s, fails, func(r failRow) string {
			return "fails naming the " + r.piece + " on " + r.why
		}, func(ctx *specs.Context, r failRow) {
			cfg := serveConfig(ctx, newTickClock(), nil)
			r.cfg(ctx, &cfg)

			srv, err := wiring.StartServer(context.Background(), cfg)

			ctx.Expect(srv == nil).To(specs.BeTrue())
			ctx.Expect(err).To(specs.Not(specs.BeNil()))
			ctx.Expect(err.Error()).To(specs.StartWith(r.piece + ": "))
			if r.is != nil {
				ctx.Expect(err).To(specs.MatchError(r.is))
			}
		})

		s.It("dispatches the bus's due retries and the outbox on each tick of the fake clock", func(ctx *specs.Context) {
			clk := newTickClock()
			dest := newDestination(false)
			cfg := serveConfig(ctx, clk, map[string]ports.Destination{"comment": dest})
			seedJob(ctx, cfg.Store)
			// The subscriber failed at the first event once already; its
			// retry is due a tick after the clock's now.
			seeded := reopen(ctx, cfg.Store)
			first := pending(ctx, seeded)[0]
			ctx.Expect(seeded.MarkFailed(context.Background(), wiring.DevServerSubscriber, first.Seq, ports.Failure{Attempts: 1, LastError: "boom", NextAttempt: serveEpoch.Add(serveTick)})).To(specs.BeNil())

			stop, done := running(ctx, cfg)
			ctx.Expect(waitAsked(ctx, clk)).ToEqual(serveTick)

			ctx.Expect(pending(ctx, seeded)).To(specs.Not(specs.BeEmpty()))
			ctx.Expect(dest.keys()).To(specs.BeEmpty())

			clk.Advance(serveTick)
			ctx.Expect(waitAsked(ctx, clk)).ToEqual(serveTick)

			ctx.Expect(pending(ctx, seeded)).To(specs.BeEmpty())
			ctx.Expect(dest.keys()).ToEqual([]string{"j-1/comment"})
			acts := actions(ctx, seeded)
			ctx.Expect(acts["j-1/comment"].Delivered).To(specs.BeTrue())
			// No destination delivers pushes yet: the push stays pending,
			// not failed.
			ctx.Expect(acts["j-1/push"].Delivered).To(specs.BeFalse())
			ctx.Expect(acts["j-1/push"].Failed).To(specs.BeFalse())
			ctx.Expect(acts["j-1/push"].Attempts).ToEqual(0)

			stop()
			ctx.Expect(<-done).To(specs.BeNil())
		})

		s.It("on shutdown waits for the tick in flight, then closes the store with its events kept", func(ctx *specs.Context) {
			clk := newTickClock()
			dest := newDestination(true)
			cfg := serveConfig(ctx, clk, map[string]ports.Destination{"comment": dest})
			seedJob(ctx, cfg.Store)
			stop, done := running(ctx, cfg)
			waitAsked(ctx, clk)
			clk.Advance(serveTick)
			<-dest.called

			stop()

			ctx.Expect(waitAsked(ctx, clk)).ToEqual(serveGrace)
			select {
			case err := <-done:
				ctx.Testing().Fatalf("Run returned %v before the tick in flight ended", err)
			default:
			}
			close(dest.release)
			ctx.Expect(<-done).To(specs.BeNil())

			st := reopen(ctx, cfg.Store)
			hist, err := st.History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.Not(specs.BeEmpty()))
			ctx.Expect(actions(ctx, st)["j-1/comment"].Delivered).To(specs.BeTrue())
		})

		s.It("cancels what still runs when the grace runs out and closes the store anyway", func(ctx *specs.Context) {
			clk := newTickClock()
			dest := newDestination(true)
			cfg := serveConfig(ctx, clk, map[string]ports.Destination{"comment": dest})
			seedJob(ctx, cfg.Store)
			stop, done := running(ctx, cfg)
			waitAsked(ctx, clk)
			clk.Advance(serveTick)
			<-dest.called

			stop()
			ctx.Expect(waitAsked(ctx, clk)).ToEqual(serveGrace)
			clk.Advance(serveGrace)

			err := <-done
			ctx.Expect(errors.Is(err, wiring.ErrShutdownGrace)).To(specs.BeTrue())
			hist, err := reopen(ctx, cfg.Store).History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(hist).To(specs.Not(specs.BeEmpty()))
		})
	})
}
