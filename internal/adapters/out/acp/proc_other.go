//go:build !unix

package acp

import "os/exec"

// ownGroup does nothing where there are no process groups.
func ownGroup(*exec.Cmd) {}

// killGroup kills only the agent where there are no process groups.
func killGroup(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
