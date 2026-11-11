//go:build linux || darwin

package checkrun

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// ownGroup runs the checks as the leader of a new process group, which the
// processes they start join unless they leave it themselves.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// group is the checks' process group, whose id is its leader's pid. The
// leader is reaped only by reap, after the last signal: until then it is a
// process, or a zombie, of factoryd's own, so neither its pid nor the
// group's id can be reused and no signal to the group reaches a stranger.
type group struct {
	cmd *exec.Cmd
	// observe, when set, is told of every signal and of the reap, in order.
	observe func(step string)
}

// untilExit returns once the leader exits, leaving it unreaped.
func (g *group) untilExit() error {
	return untilExit(g.cmd.Process.Pid)
}

// terminate asks the whole group to exit.
func (g *group) terminate() {
	_ = g.signal(syscall.SIGTERM)
}

// killNow kills the whole group, without waiting for it to be gone.
func (g *group) killNow() {
	_ = g.signal(syscall.SIGKILL)
}

// kill kills the whole group, once its leader exited, and returns when no
// process but the leader is left in it, or an error after goneWait.
func (g *group) kill() error {
	pgid := g.cmd.Process.Pid
	deadline := time.Now().Add(goneWait)
	for {
		if err := g.signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("killing process group %d: %w", pgid, err)
		}
		left, err := othersIn(pgid)
		if err != nil {
			return fmt.Errorf("listing process group %d: %w", pgid, err)
		}
		if !left {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still alive %s after it was killed", pgid, goneWait)
		}
		time.Sleep(poll)
	}
}

// reap waits for the leader and returns what Wait returned. Nothing may
// signal the group after it.
func (g *group) reap() error {
	g.step("reap")
	return g.cmd.Wait()
}

// signal sends sig to the group. A group whose only processes are zombies,
// its leader among them, refuses it with EPERM on macOS: there is no one
// left to kill.
func (g *group) signal(sig syscall.Signal) error {
	g.step(sig.String())
	err := syscall.Kill(-g.cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}

func (g *group) step(name string) {
	if g.observe != nil {
		g.observe(name)
	}
}
