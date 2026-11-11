//go:build unix

package gitdiff

import (
	"os"
	"syscall"
)

// openIndex opens the index without following a symlink at its name, and
// without blocking on a FIFO, so copyIndex can refuse either.
func openIndex(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
