package wiring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/gitworktree"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/bus"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/outbox"
	"github.com/pablogore/software-factory/internal/platform/portrange"
	"github.com/pablogore/software-factory/internal/platform/scheduler"
	"github.com/pablogore/software-factory/internal/ports"
)

// Serve errors. Callers compare with errors.Is.
var (
	// ErrNoPipelines means a configuration root has no pipeline a job
	// could follow.
	ErrNoPipelines = errors.New("no pipelines configured")
	// ErrUnknownPipeline means no loaded pipeline has the name asked for.
	ErrUnknownPipeline = errors.New("pipeline not configured")
	// ErrShutdownGrace means work was still running when the shutdown's
	// grace ran out: it was cancelled and the store closed anyway.
	ErrShutdownGrace = errors.New("work still running after the shutdown grace")
	// ErrNoCostSource means nothing tells a job's spend yet, so the
	// scheduler launches no agent rather than run one without a limit.
	ErrNoCostSource = errors.New("no cost source configured")
	// ErrNoLimitSource means nothing tells GitHub's API limit yet, so work
	// that uses GitHub never launches.
	ErrNoLimitSource = errors.New("no github limit source configured")
)

// ServeConfig is what `factoryd serve` runs with.
type ServeConfig struct {
	// Config is the configuration root, with roles.yaml, roles/ and
	// pipelines/.
	Config string
	// Store is the SQLite file jobs and their events are kept in.
	Store string
	// Repo and Base are the git repo jobs' worktrees are made in and the
	// branch they start from.
	Repo, Base string
	// DevState is where running dev servers are recorded, so the next run
	// kills what a crash left.
	DevState string
	// Concurrency and Budget are the scheduler's: how many agents run at
	// once and the USD a job may spend unless a person raised its own.
	Concurrency int
	Budget      float64
	// Tick is how often the bus's due retries and the outbox are
	// dispatched.
	Tick time.Duration
	// Grace is how long a shutdown waits for what runs before it cancels
	// it and closes the store.
	Grace time.Duration
	Clock scheduler.Clock
	// Destinations deliver the outbox's actions by kind. An action of a
	// kind none delivers stays pending, so it is not lost before its
	// destination exists.
	Destinations map[string]ports.Destination
	// Log is where a tick's failures are written.
	Log io.Writer
}

// Server is factoryd running: its store open, its bus started and its
// outbox and scheduler ready.
type Server struct {
	store  *sqlite.Store
	bus    *bus.Bus
	outbox *outbox.Deliverer
	sched  *scheduler.Scheduler
	clock  scheduler.Clock
	tick   time.Duration
	grace  time.Duration
	log    io.Writer

	// running counts the ticks in flight, which a shutdown waits for.
	running sync.WaitGroup
}

// StartServer opens cfg's store, validates its pipelines, readies the
// scheduler and the outbox, registers the subscribers and starts the bus,
// which first hands them what was pending. An error names the piece that
// failed, and whatever opened before it is closed again.
func StartServer(ctx context.Context, cfg ServeConfig) (*Server, error) {
	if cfg.Tick <= 0 || cfg.Grace <= 0 || cfg.Clock == nil || cfg.Log == nil {
		return nil, fmt.Errorf("config: %w: tick %s and grace %s must be positive, with a clock and a log", domain.ErrInvalidValue, cfg.Tick, cfg.Grace)
	}
	pipelines, err := servePipelines(cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("pipeline: %w", err)
	}
	if cfg.Store == "" {
		return nil, fmt.Errorf("store: %w: no store file", domain.ErrMissingField)
	}
	store, err := sqlite.Open(ctx, cfg.Store)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	fail := func(piece string, err error) (*Server, error) {
		return nil, errors.Join(fmt.Errorf("%s: %w", piece, err), store.Close())
	}
	sched, err := scheduler.New(cfg.Concurrency, cfg.Budget, unpricedCosts{store}, scheduler.GitHub{Limits: noLimits{}, Clock: cfg.Clock})
	if err != nil {
		return fail("scheduler", err)
	}
	servers, err := StartDevServers(ctx, cfg.DevState)
	if err != nil {
		return fail("dev servers", err)
	}
	ranges, err := portrange.New(portrange.DefaultBase, portrange.DefaultStep)
	if err != nil {
		return fail("dev servers", err)
	}
	dev := &DevServers{Servers: servers, Worktrees: gitworktree.New(cfg.Repo, cfg.Base, store), Ports: ranges, Bases: store}
	Worker(jobtoken.New(), store, pipelines, nil, gatelock.New(), cfg.Clock, dev)
	events := bus.New(store, cfg.Clock)
	if err := dev.Subscribe(events); err != nil {
		return fail("subscribers", err)
	}
	if err := events.Start(ctx); err != nil {
		return fail("bus", err)
	}
	r := &routes{Outbox: store, dests: cfg.Destinations}
	return &Server{
		store: store, bus: events, outbox: outbox.New(r, r, cfg.Clock), sched: sched,
		clock: cfg.Clock, tick: cfg.Tick, grace: cfg.Grace, log: cfg.Log,
	}, nil
}

// Run dispatches the bus and the outbox every tick until ctx ends, then
// shuts down: it starts no more ticks, waits for what runs up to the grace
// and closes the store. It returns ErrShutdownGrace when the grace ran out.
func (s *Server) Run(ctx context.Context) error {
	// Work outlives ctx, so a signal does not cut a tick short before the
	// grace runs out.
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return s.shutdown(cancel)
		case <-s.clock.After(s.tick):
		}
		done := make(chan struct{})
		s.running.Add(1)
		go func() {
			defer s.running.Done()
			defer close(done)
			s.dispatch(work)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			return s.shutdown(cancel)
		}
	}
}

// dispatch hands the bus's due events and the outbox's due actions on once.
// A failure is logged, not returned: the next tick tries again.
func (s *Server) dispatch(ctx context.Context) {
	if err := s.bus.Dispatch(ctx); err != nil {
		_, _ = fmt.Fprintln(s.log, "tick:", err)
	}
	if err := s.outbox.Deliver(ctx); err != nil {
		_, _ = fmt.Fprintln(s.log, "tick:", err)
	}
}

// drainPoll is how often a shutdown checks whether the scheduler's agents
// are done.
const drainPoll = 50 * time.Millisecond

// shutdown waits for the ticks in flight and the scheduler's running agents
// up to the grace, cancels them with cancel if it runs out, and closes the
// store.
func (s *Server) shutdown(cancel context.CancelFunc) error {
	drained := make(chan struct{})
	go func() {
		s.running.Wait()
		for s.sched.Running() > 0 {
			time.Sleep(drainPoll)
		}
		close(drained)
	}()
	var err error
	select {
	case <-drained:
	case <-s.clock.After(s.grace):
		cancel()
		err = fmt.Errorf("shutdown: %s: %w", s.grace, ErrShutdownGrace)
	}
	if cerr := s.store.Close(); cerr != nil {
		err = errors.Join(err, fmt.Errorf("store: %w", cerr))
	}
	return err
}

// servePipelines loads dir's roles and pipelines, which must name at least
// one pipeline, each under its own name.
func servePipelines(dir string) (loadedPipelines, error) {
	roles, err := loadRoles(dir)
	if err != nil {
		return nil, err
	}
	ps, err := loadPipelines(dir, roles)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("%w: no pipelines/*.yaml in %s", ErrNoPipelines, dir)
	}
	byName := loadedPipelines{}
	for _, p := range ps {
		if _, ok := byName[p.Name]; ok {
			return nil, fmt.Errorf("%w: two pipelines named %q", domain.ErrInvalidValue, p.Name)
		}
		byName[p.Name] = p
	}
	return byName, nil
}

// loadedPipelines are the pipelines serve loaded, by name.
type loadedPipelines map[string]domain.Pipeline

func (l loadedPipelines) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	p, ok := l[name]
	if !ok {
		return domain.Pipeline{}, fmt.Errorf("pipeline %q: %w", name, ErrUnknownPipeline)
	}
	return p, nil
}

// routes is the outbox as its deliverer sees it and the destination it
// delivers to: it lists as due only the actions of a kind dests delivers,
// and hands each to its kind's destination.
type routes struct {
	ports.Outbox
	dests map[string]ports.Destination

	// kinds is the kind of each action of the last due list, since Done
	// is asked by key alone. The deliverer runs one Deliver at a time.
	kinds map[string]string
}

func (r *routes) DueActions(ctx context.Context, now time.Time) ([]ports.QueuedAction, error) {
	due, err := r.Outbox.DueActions(ctx, now)
	if err != nil {
		return nil, err
	}
	r.kinds = map[string]string{}
	var routed []ports.QueuedAction
	for _, q := range due {
		if _, ok := r.dests[q.Kind]; ok {
			r.kinds[q.Key] = q.Kind
			routed = append(routed, q)
		}
	}
	return routed, nil
}

func (r *routes) Deliver(ctx context.Context, a ports.Action) error {
	return r.dests[a.Kind].Deliver(ctx, a)
}

func (r *routes) Done(ctx context.Context, key string) (bool, error) {
	return r.dests[r.kinds[key]].Done(ctx, key)
}

// unpricedCosts reads a job's own budget from the store, but nothing tells
// its spend yet, so Spent fails and the scheduler never launches an agent
// without a limit.
type unpricedCosts struct{ jobs ports.JobStore }

func (unpricedCosts) Spent(_ context.Context, job domain.JobID) (float64, error) {
	return 0, fmt.Errorf("job %q: %w", job, ErrNoCostSource)
}

func (c unpricedCosts) Budget(ctx context.Context, job domain.JobID) (float64, error) {
	j, _, err := c.jobs.Load(ctx, job)
	return j.Budget, err
}

// noLimits tells no GitHub limit yet, so work that uses GitHub never
// launches.
type noLimits struct{}

func (noLimits) Limit(context.Context) (scheduler.RateLimit, error) {
	return scheduler.RateLimit{}, ErrNoLimitSource
}
