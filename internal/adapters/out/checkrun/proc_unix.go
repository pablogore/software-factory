//go:build unix

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

// terminate asks the checks' whole group to exit.
func terminate(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// killGroup kills the checks' whole group, once its leader is reaped too,
// and returns when no process of it is left, or an error after goneWait.
// Until then the group's id can't be reused, so the signal never reaches
// a stranger.
func killGroup(cmd *exec.Cmd) error {
	pgid := cmd.Process.Pid
	deadline := time.Now().Add(goneWait)
	for {
		err := syscall.Kill(-pgid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("killing process group %d: %w", pgid, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still alive %s after it was killed", pgid, goneWait)
		}
		time.Sleep(poll)
	}
}
