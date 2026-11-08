package worker_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/ports"
)

// servers keeps the dev servers started, by job and stage, as running.
type servers struct {
	mu      sync.Mutex
	running map[string]int
	started []string
}

func (f *servers) Start(_ context.Context, wt ports.Worktree, stage string, port int, _ ports.DevCommand) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := string(wt.Job) + " " + stage
	f.running[k] = port
	f.started = append(f.started, k)
	return nil
}

func (f *servers) StopStage(_ context.Context, job domain.JobID, stage string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, string(job)+" "+stage)
	return nil
}

func (f *servers) live() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[string]int{}
	for k, v := range f.running {
		m[k] = v
	}
	return m
}

type sameDir struct{}

func (sameDir) Prepare(_ context.Context, job domain.JobID) (ports.Worktree, error) {
	return ports.Worktree{Job: job, Path: "/wt/" + string(job)}, nil
}

// firstFree hands out 5100 and up, skipping the ports taken.
type firstFree struct{}

func (firstFree) Free(_ domain.JobID, taken []int) (int, error) {
	p := 5100
	for slices.Contains(taken, p) {
		p++
	}
	return p, nil
}

type command []string

func (c command) DevCommand(context.Context, domain.JobID, ports.Worktree) (ports.DevCommand, error) {
	return ports.DevCommand{Args: c}, nil
}

// serving is the tools of a role that may run the job's dev server.
var serving = []string{"submit_result", "progress", "dev_server"}

// withDev is w with dev servers run by the returned fake, with cmd as the
// repo's command.
func withDev(w world, cmd command) (world, *servers) {
	f := &servers{running: map[string]int{}}
	w.sub.Servers, w.sub.Worktrees, w.sub.Ports, w.sub.DevCommands = f, sameDir{}, firstFree{}, cmd
	return w, f
}

type refusedDevRow struct {
	name  string
	grant jobtoken.Grant
}

func TestDevServer(t *testing.T) {
	specs.Describe(t, "running the job's dev server with a job token", func(s *specs.Spec) {
		builder := jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: serving, Round: 1}

		s.It("starts the builder's server on a port of the job and returns it, saving nothing", func(ctx *specs.Context) {
			w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
			token := w.issue(ctx, builder)

			port, err := w.sub.DevServer(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(port).ToEqual(5100)
			ctx.Expect(f.live()).ToEqual(map[string]int{"j-1 build/builder": 5100})
			w.untouched(ctx, "j-1")
		})

		s.It("returns the same server to a second call of the session", func(ctx *specs.Context) {
			w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
			token := w.issue(ctx, builder)

			first, _ := w.sub.DevServer(context.Background(), token, "j-1")
			second, err := w.sub.DevServer(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(second).ToEqual(first)
			ctx.Expect(f.started).To(specs.HaveLen(1))
		})

		s.It("gives two sessions of the job different ports", func(ctx *specs.Context) {
			w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
			ux := w.issue(ctx, jobtoken.Grant{Job: "j-1", Role: "reviewer-ux", Kind: domain.KindReview, Tools: serving, Round: 1})

			_, _ = w.sub.DevServer(context.Background(), w.issue(ctx, builder), "j-1")
			port, err := w.sub.DevServer(context.Background(), ux, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(port).ToEqual(5101)
			ctx.Expect(f.live()).To(specs.HaveLen(2))
		})

		s.It("stops the server when the role asks", func(ctx *specs.Context) {
			w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
			token := w.issue(ctx, builder)
			_, _ = w.sub.DevServer(context.Background(), token, "j-1")

			err := w.sub.StopDevServer(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(f.live()).To(specs.BeEmpty())
		})

		s.It("stops the server once the role's result is saved", func(ctx *specs.Context) {
			w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
			token := w.issue(ctx, builder)
			_, _ = w.sub.DevServer(context.Background(), token, "j-1")

			_, err := w.sub.Submit(context.Background(), token, "j-1", []byte(`{"status":"READY"}`))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(f.live()).To(specs.BeEmpty())
		})

		s.It("stops a server left from an earlier round before starting the new one", func(ctx *specs.Context) {
			w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
			_, _ = w.sub.DevServer(context.Background(), w.issue(ctx, builder), "j-1")
			w.newRound(ctx, "j-1")
			job, _, err := w.store.Load(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			hist, err := w.store.History(context.Background(), "j-1")
			ctx.Expect(err).To(specs.BeNil())
			token := w.issue(ctx, jobtoken.For(job, hist, domain.Role{Name: "builder", Kind: domain.KindBuild, Tools: serving}))

			port, err := w.sub.DevServer(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(port).ToEqual(5100)
			ctx.Expect(f.started).To(specs.HaveLen(2))
			ctx.Expect(f.live()).To(specs.HaveLen(1))
		})

		s.It("refuses with a clear error and starts nothing when the repo configures no command", func(ctx *specs.Context) {
			w, f := withDev(newWorld(ctx), nil)

			_, err := w.sub.DevServer(context.Background(), w.issue(ctx, builder), "j-1")

			ctx.Expect(err).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(err.Error()).To(specs.Contain("the repo configures no dev server command"))
			ctx.Expect(f.started).To(specs.BeEmpty())
		})

		s.When("the job's events come", func(s *specs.Spec) {
			ux := jobtoken.Grant{Job: "j-1", Role: "reviewer-ux", Kind: domain.KindReview, Tools: serving, Round: 1}

			s.It("stops the server of the role whose attempt failed, and only that one", func(ctx *specs.Context) {
				w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
				_, _ = w.sub.DevServer(context.Background(), w.issue(ctx, builder), "j-1")
				_, _ = w.sub.DevServer(context.Background(), w.issue(ctx, ux), "j-1")

				err := w.sub.StopLeft(context.Background(), domain.AttemptFailed{Job: "j-1", Role: "builder", Reason: "no result", At: at})

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(f.live()).ToEqual(map[string]int{"j-1 build/reviewer-ux": 5101})
			})

			s.It("stops every server of the job once it left the stage they were started in", func(ctx *specs.Context) {
				w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
				_, _ = w.sub.DevServer(context.Background(), w.issue(ctx, builder), "j-1")
				_, _ = w.sub.DevServer(context.Background(), w.issue(ctx, ux), "j-1")
				ready := domain.ResultSubmitted{Job: "j-1", Role: "builder", Result: domain.BuildResult{Status: domain.BuildReady}, At: at}
				w.apply(ctx, "j-1", ready)

				err := w.sub.StopLeft(context.Background(), domain.StageEntered{Job: "j-1", Stage: domain.StageGate, At: at})

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(f.live()).To(specs.BeEmpty())
			})

			s.It("leaves the servers of the stage the job is still in, however often an event comes", func(ctx *specs.Context) {
				w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
				_, _ = w.sub.DevServer(context.Background(), w.issue(ctx, builder), "j-1")
				other := domain.AttemptFailed{Job: "j-1", Role: "reviewer-ux", Reason: "no result", At: at}

				ctx.Expect(w.sub.StopLeft(context.Background(), other)).To(specs.BeNil())
				ctx.Expect(w.sub.StopLeft(context.Background(), other)).To(specs.BeNil())

				ctx.Expect(f.live()).ToEqual(map[string]int{"j-1 build/builder": 5100})
			})
		})

		refused := []refusedDevRow{
			{"a builder without a tools list", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Round: 1}},
			{"a builder whose tools do not list dev_server", jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: gating, Round: 1}},
			{"reviewer-security, even listing dev_server", jobtoken.Grant{Job: "j-1", Role: "reviewer-security", Kind: domain.KindReview, Tools: serving, Round: 1}},
			{"reviewer-code, even listing dev_server", jobtoken.Grant{Job: "j-1", Role: "reviewer-code", Kind: domain.KindReview, Tools: serving, Round: 1}},
			{"an approver, even listing dev_server", jobtoken.Grant{Job: "j-1", Role: "approver", Kind: domain.KindApprove, Tools: serving, Round: 1}},
		}
		specs.Table(s, refused, func(r refusedDevRow) string { return "refuses " + r.name + ", starting nothing" }, func(ctx *specs.Context, r refusedDevRow) {
			w, f := withDev(newWorld(ctx), command{"npm", "run", "dev"})
			token := w.issue(ctx, r.grant)

			port, err := w.sub.DevServer(context.Background(), token, "j-1")

			ctx.Expect(err).To(specs.MatchError(jobtoken.ErrForbidden))
			ctx.Expect(err.Error()).To(specs.Contain(`may not use tool "dev_server"`))
			ctx.Expect(port).ToEqual(0)
			ctx.Expect(f.started).To(specs.BeEmpty())
			w.untouched(ctx, "j-1")
		})
	})
}
