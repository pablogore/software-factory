//go:build unix

package acp

import (
	"os/exec"
	"syscall"
)

// ownGroup launches the agent as the leader of a new process group, which
// its child processes join unless they leave it themselves.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills the agent's process group: the agent and every child
// still in it, even once the agent itself has exited.
func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
