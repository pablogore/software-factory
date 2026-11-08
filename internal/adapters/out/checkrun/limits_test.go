//go:build unix

package checkrun_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/checkrun"
)

// alive reports whether the process pid still exists.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// pidIn reads the pid a check wrote to file.
func pidIn(ctx *specs.Context, file string) int {
	data, err := os.ReadFile(file)
	ctx.Expect(err).To(specs.BeNil())
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	ctx.Expect(err).To(specs.BeNil())
	return pid
}

func TestRunnerGroup(t *testing.T) {
	specs.Describe(t, "checkrun.Runner and the checks' process group", func(s *specs.Spec) {
		bg := context.Background()

		s.It("kills a child the checks left in the background, its output redirected, before it returns", func(ctx *specs.Context) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "child.pid")

			start := time.Now()
			run, err := checkrun.Runner{}.Run(bg, dir, []string{"sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $! > " + pidFile + "; echo done"})

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Passed()).To(specs.BeTrue())
			ctx.Expect(run.Output).ToEqual("done\n")
			ctx.Expect(time.Since(start) < 5*time.Second).To(specs.BeTrue())
			ctx.Expect(alive(pidIn(ctx, pidFile))).To(specs.BeFalse())
		})

		s.It("does not wait for a child in the background that still holds the output", func(ctx *specs.Context) {
			start := time.Now()
			run, err := checkrun.Runner{}.Run(bg, t.TempDir(), []string{"sh", "-c", "sleep 30 & echo done"})

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Output).ToEqual("done\n")
			ctx.Expect(time.Since(start) < 5*time.Second).To(specs.BeTrue())
		})

		s.When("ctx ends while the checks run", func(s *specs.Spec) {
			s.It("stops them with SIGTERM to their group and returns once no process of it is left", func(ctx *specs.Context) {
				dir := t.TempDir()
				pidFile := filepath.Join(dir, "child.pid")
				cancelled, cancel := context.WithCancel(bg)
				go func() {
					for {
						if _, err := os.Stat(pidFile); err == nil {
							cancel()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}()

				_, err := checkrun.Runner{}.Run(cancelled, dir, []string{"sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $! > " + pidFile + "; wait"})

				ctx.Expect(err).To(specs.MatchError(context.Canceled))
				ctx.Expect(alive(pidIn(ctx, pidFile))).To(specs.BeFalse())
			})

			s.It("kills with SIGKILL a group that ignores SIGTERM, after the grace period", func(ctx *specs.Context) {
				dir := t.TempDir()
				pidFile := filepath.Join(dir, "child.pid")
				cancelled, cancel := context.WithCancel(bg)
				go func() {
					for {
						if _, err := os.Stat(pidFile); err == nil {
							cancel()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}()
				start := time.Now()

				_, err := checkrun.Runner{}.Run(cancelled, dir, []string{"sh", "-c", "trap '' TERM; sleep 30 >/dev/null 2>&1 & echo $! > " + pidFile + "; wait"})

				ctx.Expect(err).To(specs.MatchError(context.Canceled))
				ctx.Expect(time.Since(start) < 10*time.Second).To(specs.BeTrue())
				ctx.Expect(alive(pidIn(ctx, pidFile))).To(specs.BeFalse())
			})
		})
	})
}

func TestRunnerLimits(t *testing.T) {
	specs.Describe(t, "checkrun.Runner with limits", func(s *specs.Spec) {
		bg := context.Background()

		s.When("the run takes longer than its timeout", func(s *specs.Spec) {
			s.It("stops the command with its process group and fails the run, saying so", func(ctx *specs.Context) {
				dir := t.TempDir()
				pidFile := filepath.Join(dir, "child.pid")
				// The child sleeps in the background, in the command's
				// group, with its output away from the run's pipe.
				script := "sleep 30 >/dev/null 2>&1 & echo $! > " + pidFile + "; echo started; wait"

				start := time.Now()
				run, err := checkrun.Runner{Timeout: 300 * time.Millisecond}.Run(bg, dir, []string{"sh", "-c", script})

				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(time.Since(start) < 10*time.Second).To(specs.BeTrue())
				ctx.Expect(run.Passed()).To(specs.BeFalse())
				ctx.Expect(run.ExitCode).ToEqual(-1)
				ctx.Expect(run.Output).ToEqual("started\n")
				ctx.Expect(run.Notes).ToEqual([]string{"timed out: the checks ran over 300ms and were stopped with their process group"})
				ctx.Expect(alive(pidIn(ctx, pidFile))).To(specs.BeFalse())
			})
		})

		s.It("keeps a run within its timeout as it is", func(ctx *specs.Context) {
			run, err := checkrun.Runner{Timeout: time.Minute}.Run(bg, t.TempDir(), []string{"sh", "-c", "echo ok"})
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Passed()).To(specs.BeTrue())
			ctx.Expect(run.Notes).To(specs.BeEmpty())
		})

		s.It("is still an error, not a timed out run, when ctx ends first", func(ctx *specs.Context) {
			short, cancel := context.WithTimeout(bg, 200*time.Millisecond)
			defer cancel()
			_, err := checkrun.Runner{Timeout: time.Minute}.Run(short, t.TempDir(), []string{"sleep", "30"})
			ctx.Expect(err).To(specs.MatchError(context.DeadlineExceeded))
		})

		s.When("the output is longer than its limit", func(s *specs.Spec) {
			s.It("keeps only the first bytes and says how many it dropped", func(ctx *specs.Context) {
				run, err := checkrun.Runner{MaxOutput: 10}.Run(bg, t.TempDir(), []string{"sh", "-c", "printf 0123456789abcdef; echo err >&2"})
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(run.Output).ToEqual("0123456789")
				ctx.Expect(run.Passed()).To(specs.BeTrue())
				ctx.Expect(run.Notes).ToEqual([]string{"output truncated: kept the first 10 bytes, dropped the other 10"})
			})

			s.It("drops it as it comes, so a long output never fills memory", func(ctx *specs.Context) {
				run, err := checkrun.Runner{MaxOutput: 1024}.Run(bg, t.TempDir(), []string{"sh", "-c", "head -c 20000000 /dev/zero"})
				ctx.Expect(err).To(specs.BeNil())
				ctx.Expect(len(run.Output)).ToEqual(1024)
				ctx.Expect(run.Notes).ToEqual([]string{"output truncated: kept the first 1024 bytes, dropped the other 19998976"})
			})
		})

		s.It("keeps an output within its limit whole, with no note", func(ctx *specs.Context) {
			run, err := checkrun.Runner{MaxOutput: 10}.Run(bg, t.TempDir(), []string{"sh", "-c", "printf 0123456789"})
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(run.Output).ToEqual("0123456789")
			ctx.Expect(run.Notes).To(specs.BeEmpty())
		})
	})
}
