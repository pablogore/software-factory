//go:build unix

package checkrun

import (
	"os/exec"
	"syscall"
)

// ownGroup runs the checks as the leader of a new process group, which the
// processes they start join unless they leave it themselves.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
}
