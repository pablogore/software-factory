//go:build unix

package gitdiff

import (
	"os"
	"syscall"
)

// indexFlags open the index without following a symlink at its name, and
// without blocking on a FIFO, so copyIndex can refuse either; and without
// taking a terminal there as factoryd's controlling one, whose hangup and
// job-control signals the builder holding it could then send factoryd.
const indexFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_NOCTTY

// openIndex opens the index with indexFlags.
func openIndex(name string) (*os.File, error) {
	return os.OpenFile(name, indexFlags, 0)
}
