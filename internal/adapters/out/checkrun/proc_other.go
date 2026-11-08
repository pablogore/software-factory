//go:build !unix

package checkrun

import "os/exec"

// ownGroup leaves the default, killing only the checks' own process, where
// there are no process groups.
func ownGroup(*exec.Cmd) {}
