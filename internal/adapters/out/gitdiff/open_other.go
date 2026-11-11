//go:build !unix

package gitdiff

import "os"

// openIndex opens the index; copyIndex still refuses anything but a regular
// file.
func openIndex(name string) (*os.File, error) {
	return os.Open(name)
}
