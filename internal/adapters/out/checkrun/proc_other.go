//go:build !linux && !darwin

package checkrun

import "os/exec"

// ownGroup does nothing where the checks' group is not killed.
func ownGroup(*exec.Cmd) {}

// group is only the checks' own process where there is no way to tell that
// a process group's id is still the run's.
type group struct {
	cmd *exec.Cmd
	// observe, when set, is told of the reap.
	observe func(step string)
	waited  error
}

// untilExit waits for the checks' process, which also reaps it.
func (g *group) untilExit() error {
	g.waited = g.cmd.Wait()
	return nil
}

// terminate kills only the checks' own process.
func (g *group) terminate() {
	_ = g.cmd.Process.Kill()
}

// killNow kills only the checks' own process.
func (g *group) killNow() {
	_ = g.cmd.Process.Kill()
}

// kill kills only the checks' own process.
func (g *group) kill() error {
	_ = g.cmd.Process.Kill()
	return nil
}

// reap returns what Wait returned in untilExit.
func (g *group) reap() error {
	if g.observe != nil {
		g.observe("reap")
	}
	return g.waited
}
