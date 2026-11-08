//go:build !unix

package checkrun

import "os/exec"

// ownGroup does nothing where there are no process groups.
func ownGroup(*exec.Cmd) {}

// terminate kills only the checks' own process where there are no process
// groups.
func terminate(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}

// killGroup kills only the checks' own process where there are no process
// groups.
func killGroup(cmd *exec.Cmd) error {
	_ = cmd.Process.Kill()
	return nil
}
