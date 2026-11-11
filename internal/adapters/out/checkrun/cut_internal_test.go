//go:build linux || darwin

package checkrun

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/platform/childenv"
)

// cutRow is a way a run ends: by itself, cut by its ctx or past its timeout.
type cutRow struct {
	name    string
	timeout time.Duration
	cancel  bool
	script  string
}

func cutRows() []cutRow {
	return []cutRow{
		{name: "ending by itself, with a child left in the background", script: "sleep 30 >/dev/null 2>&1 & echo done"},
		{name: "cut by its ctx, as when the job's result arrives", cancel: true, script: "sleep 30 >/dev/null 2>&1 & wait"},
		{name: "cut past its timeout", timeout: 200 * time.Millisecond, script: "sleep 30 >/dev/null 2>&1 & wait"},
		{name: "cut past its timeout, ignoring SIGTERM", timeout: 200 * time.Millisecond, script: "trap '' TERM; sleep 30 >/dev/null 2>&1 & wait"},
	}
}

// started starts script as the leader of its own group, as Run does.
func started(ctx *specs.Context, script string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", script)
	ownGroup(cmd)
	ctx.Expect(cmd.Start()).To(specs.BeNil())
	return cmd
}

func TestDemoCutSignalsOnlyItsOwnGroup(t *testing.T) {
	specs.Describe(t, "a cut run's signals", func(s *specs.Spec) {
		specs.Table(s, cutRows(), func(r cutRow) string { return "are all sent before its leader is reaped, " + r.name }, func(ctx *specs.Context, r cutRow) {
			var steps []string
			runner := Runner{Homes: childenv.Homes{Root: t.TempDir()}, Timeout: r.timeout, observe: func(step string) { steps = append(steps, step) }}
			run := context.Background()
			if r.cancel {
				var cancel context.CancelFunc
				run, cancel = context.WithTimeout(run, 200*time.Millisecond)
				defer cancel()
			}

			_, _ = runner.Run(run, "j-1", t.TempDir(), []string{"sh", "-c", r.script})

			ctx.Expect(len(steps) >= 2).To(specs.BeTrue())
			ctx.Expect(steps[len(steps)-1]).ToEqual("reap")
			for _, step := range steps[:len(steps)-1] {
				ctx.Expect(step == "reap").To(specs.BeFalse())
			}
		})
	})

	specs.Describe(t, "untilExit", func(s *specs.Spec) {
		s.It("returns once the leader exits and leaves it a zombie whose pid stays taken, for Wait to reap", func(ctx *specs.Context) {
			cmd := started(ctx, "sleep 0.2; exit 3")

			ctx.Expect(untilExit(cmd.Process.Pid)).To(specs.BeNil())

			ctx.Expect(syscall.Kill(cmd.Process.Pid, 0)).To(specs.BeNil())
			exit, ok := errors.AsType[*exec.ExitError](cmd.Wait())
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(exit.ExitCode()).ToEqual(3)
		})

		s.It("returns at once for a leader that already exited", func(ctx *specs.Context) {
			cmd := started(ctx, "exit 4")
			time.Sleep(200 * time.Millisecond)

			ctx.Expect(untilExit(cmd.Process.Pid)).To(specs.BeNil())

			exit, ok := errors.AsType[*exec.ExitError](cmd.Wait())
			ctx.Expect(ok).To(specs.BeTrue())
			ctx.Expect(exit.ExitCode()).ToEqual(4)
		})
	})

	specs.Describe(t, "othersIn", func(s *specs.Spec) {
		s.It("sees a process the leader left in its group, and none once the group is killed, while the leader is still unreaped", func(ctx *specs.Context) {
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			cmd := started(ctx, "sleep 30 >/dev/null 2>&1 & echo $! > "+pidFile)
			g := &group{cmd: cmd}
			ctx.Expect(g.untilExit()).To(specs.BeNil())

			left, err := othersIn(cmd.Process.Pid)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(left).To(specs.BeTrue())

			ctx.Expect(g.kill()).To(specs.BeNil())
			left, err = othersIn(cmd.Process.Pid)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(left).To(specs.BeFalse())
			ctx.Expect(g.reap()).To(specs.BeNil())
		})

		s.It("sees no process in a group that has only its leader", func(ctx *specs.Context) {
			cmd := started(ctx, "exec sleep 30")
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

			left, err := othersIn(cmd.Process.Pid)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(left).To(specs.BeFalse())
		})
	})
}
