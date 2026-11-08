//go:build unix

package devserver_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/devserver"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/portrange"
	"github.com/pablogore/software-factory/internal/ports"
)

// tree starts a child that starts a grandchild, each writing its pid to the
// worktree, and waits for them.
const tree = `echo $$ > leader.pid
sh -c 'echo $$ > child.pid; sleep 300 & echo $! > grandchild.pid; wait' &
wait`

// stubborn is tree ignoring SIGTERM in every process, so only SIGKILL stops it.
const stubborn = "trap '' TERM\n" + tree

var treePids = []string{"leader.pid", "child.pid", "grandchild.pid"}

func sh(script string) ports.DevCommand { return ports.DevCommand{Args: []string{"sh", "-c", script}} }

// newServers returns Servers whose leftovers are killed when the test ends.
func newServers(ctx *specs.Context, grace time.Duration, jobs ...domain.JobID) *devserver.Servers {
	srv := devserver.New(grace)
	ctx.Testing().Cleanup(func() {
		for _, job := range jobs {
			_ = srv.StopJob(context.Background(), job)
		}
	})
	return srv
}

func worktree(ctx *specs.Context, job domain.JobID) ports.Worktree {
	return ports.Worktree{Job: job, Path: ctx.Testing().TempDir(), Branch: "factory/" + string(job)}
}

// eventually reports whether ok holds within a few seconds.
func eventually(ok func() bool) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return true
		}
	}
	return ok()
}

func readFile(dir, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || !strings.HasSuffix(string(b), "\n") {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// pids waits for the server in dir to write every pid file in names.
func pids(ctx *specs.Context, dir string, names ...string) []int {
	ctx.Testing().Helper()
	var got []int
	ctx.Expect(eventually(func() bool {
		got = got[:0]
		for _, name := range names {
			s, ok := readFile(dir, name)
			if !ok {
				return false
			}
			pid, err := strconv.Atoi(s)
			if err != nil {
				return false
			}
			got = append(got, pid)
		}
		return true
	})).To(specs.BeTrue())
	return got
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func noneAlive(pids []int) bool {
	for _, pid := range pids {
		if alive(pid) {
			return false
		}
	}
	return true
}

func allAlive(pids []int) bool {
	for _, pid := range pids {
		if !alive(pid) {
			return false
		}
	}
	return true
}

func TestDevServers(t *testing.T) {
	specs.Describe(t, "devserver.Servers", func(s *specs.Spec) {
		bg := context.Background()
		const job domain.JobID = "job-1"

		s.It("runs the dev server in the job's worktree with a port of the job's range in PORT", func(ctx *specs.Context) {
			srv := newServers(ctx, devserver.DefaultGrace, job)
			ranges, err := portrange.New(portrange.DefaultBase, portrange.DefaultStep)
			ctx.Expect(err).To(specs.BeNil())
			_, err = ranges.Acquire("job-0")
			ctx.Expect(err).To(specs.BeNil())
			r, err := ranges.Acquire(job)
			ctx.Expect(err).To(specs.BeNil())
			wt := worktree(ctx, job)

			err = srv.Start(bg, wt, "build", r.First, sh("pwd -P > where; echo $PORT > port; exec sleep 300"))

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(eventually(func() bool { _, ok := readFile(wt.Path, "port"); return ok })).To(specs.BeTrue())
			where, _ := readFile(wt.Path, "where")
			want, err := filepath.EvalSymlinks(wt.Path)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(where).ToEqual(want)
			port, _ := readFile(wt.Path, "port")
			ctx.Expect(port).ToEqual(strconv.Itoa(r.First))
			ctx.Expect(port).ToEqual("5110")
		})

		s.It("stops the stage's dev server with its child and grandchild, leaving other stages and jobs running", func(ctx *specs.Context) {
			srv := newServers(ctx, devserver.DefaultGrace, job, "job-2")
			wt, other, otherJob := worktree(ctx, job), worktree(ctx, job), worktree(ctx, "job-2")
			ctx.Expect(srv.Start(bg, wt, "build", 5100, sh(tree))).To(specs.BeNil())
			ctx.Expect(srv.Start(bg, other, "review", 5101, sh(tree))).To(specs.BeNil())
			ctx.Expect(srv.Start(bg, otherJob, "build", 5110, sh(tree))).To(specs.BeNil())
			stopped := pids(ctx, wt.Path, treePids...)
			kept := append(pids(ctx, other.Path, treePids...), pids(ctx, otherJob.Path, treePids...)...)

			err := srv.StopStage(bg, job, "build")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(noneAlive(stopped)).To(specs.BeTrue())
			ctx.Expect(allAlive(kept)).To(specs.BeTrue())
		})

		s.It("stops every dev server of a job when the job ends, children included, leaving other jobs running", func(ctx *specs.Context) {
			srv := newServers(ctx, devserver.DefaultGrace, job, "job-2")
			build, review, otherJob := worktree(ctx, job), worktree(ctx, job), worktree(ctx, "job-2")
			ctx.Expect(srv.Start(bg, build, "build", 5100, sh(tree))).To(specs.BeNil())
			ctx.Expect(srv.Start(bg, review, "review", 5101, sh(tree))).To(specs.BeNil())
			ctx.Expect(srv.Start(bg, otherJob, "build", 5110, sh(tree))).To(specs.BeNil())
			stopped := append(pids(ctx, build.Path, treePids...), pids(ctx, review.Path, treePids...)...)
			kept := pids(ctx, otherJob.Path, treePids...)

			err := srv.StopJob(bg, job)

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(noneAlive(stopped)).To(specs.BeTrue())
			ctx.Expect(allAlive(kept)).To(specs.BeTrue())
		})

		s.It("kills a dev server that ignores SIGTERM, with its children, once the grace period is over", func(ctx *specs.Context) {
			const grace = 300 * time.Millisecond
			srv := newServers(ctx, grace, job)
			wt := worktree(ctx, job)
			ctx.Expect(srv.Start(bg, wt, "build", 5100, sh(stubborn))).To(specs.BeNil())
			running := pids(ctx, wt.Path, treePids...)
			start := time.Now()

			err := srv.StopStage(bg, job, "build")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(time.Since(start) >= grace).To(specs.BeTrue())
			ctx.Expect(noneAlive(running)).To(specs.BeTrue())
		})

		s.It("stops the grandchild a dev server left behind when it exited on its own", func(ctx *specs.Context) {
			srv := newServers(ctx, devserver.DefaultGrace, job)
			wt := worktree(ctx, job)
			ctx.Expect(srv.Start(bg, wt, "build", 5100, sh("echo $$ > leader.pid; sleep 300 & echo $! > grandchild.pid"))).To(specs.BeNil())
			running := pids(ctx, wt.Path, "leader.pid", "grandchild.pid")
			ctx.Expect(eventually(func() bool { return !alive(running[0]) })).To(specs.BeTrue())
			ctx.Expect(alive(running[1])).To(specs.BeTrue())

			err := srv.StopStage(bg, job, "build")

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(noneAlive(running)).To(specs.BeTrue())
		})

		s.It("gives the dev server only PATH, HOME, LANG, TMPDIR, the repo's variables and PORT of factoryd's environment", func(ctx *specs.Context) {
			ctx.Testing().Setenv("FACTORY_TEST_SECRET", "s3cret")
			ctx.Testing().Setenv("GITHUB_TOKEN", "ghp_secret")
			srv := newServers(ctx, devserver.DefaultGrace, job)
			wt := worktree(ctx, job)
			dc := sh("env > env.txt; echo >> env.txt; exec sleep 300")
			dc.Env = map[string]string{"NODE_ENV": "development", "PORT": "1"}

			ctx.Expect(srv.Start(bg, wt, "build", 5100, dc)).To(specs.BeNil())

			ctx.Expect(eventually(func() bool { _, ok := readFile(wt.Path, "env.txt"); return ok })).To(specs.BeTrue())
			got, _ := readFile(wt.Path, "env.txt")
			var names []string
			for _, line := range strings.Split(got, "\n") {
				if name, _, ok := strings.Cut(line, "="); ok {
					names = append(names, name)
				}
			}
			ctx.Expect(got).To(specs.Not(specs.Contain("s3cret")))
			ctx.Expect(got).To(specs.Not(specs.Contain("ghp_secret")))
			ctx.Expect(got).To(specs.Contain("NODE_ENV=development"))
			ctx.Expect(got).To(specs.Contain("PORT=5100"))
			for _, name := range names {
				ctx.Expect([]string{"PATH", "HOME", "LANG", "TMPDIR", "NODE_ENV", "PORT", "PWD", "SHLVL", "_", "OLDPWD"}).To(specs.Contain(name))
			}
		})

		s.It("refuses a second dev server for a stage until the first is stopped", func(ctx *specs.Context) {
			srv := newServers(ctx, devserver.DefaultGrace, job)
			wt := worktree(ctx, job)
			ctx.Expect(srv.Start(bg, wt, "build", 5100, sh("exec sleep 300"))).To(specs.BeNil())

			ctx.Expect(srv.Start(bg, wt, "build", 5100, sh("exec sleep 300"))).To(specs.MatchError(devserver.ErrRunning))
			ctx.Expect(srv.StopStage(bg, job, "build")).To(specs.BeNil())
			ctx.Expect(srv.Start(bg, wt, "build", 5100, sh("exec sleep 300"))).To(specs.BeNil())
		})

		s.It("stopping a stage or a job with no dev server is harmless", func(ctx *specs.Context) {
			srv := newServers(ctx, devserver.DefaultGrace)

			ctx.Expect(srv.StopStage(bg, job, "build")).To(specs.BeNil())
			ctx.Expect(srv.StopJob(bg, job)).To(specs.BeNil())
		})

		s.It("refuses to start without a command, a worktree or a valid port", func(ctx *specs.Context) {
			srv := newServers(ctx, devserver.DefaultGrace, job)
			wt := worktree(ctx, job)

			ctx.Expect(srv.Start(bg, wt, "build", 5100, ports.DevCommand{})).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(srv.Start(bg, ports.Worktree{Job: job}, "build", 5100, sh("true"))).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(srv.Start(bg, wt, "", 5100, sh("true"))).To(specs.MatchError(domain.ErrMissingField))
			ctx.Expect(srv.Start(bg, wt, "build", 0, sh("true"))).To(specs.MatchError(domain.ErrInvalidValue))
		})
	})
}
