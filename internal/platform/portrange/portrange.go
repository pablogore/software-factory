// Package portrange hands each open job its own range of ports, so dev
// servers of jobs running at once never fight over one. Range n is step ports
// from base + step × n; a job gets the smallest n no open job holds, and a
// released n is handed out again, so n stays as small as the number of jobs
// open at once rather than growing with every job ever run.
//
// Ranges live in memory only: after a restart every range is free again.
package portrange

import (
	"errors"
	"fmt"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
)

const (
	// DefaultBase is the first port the factory on main uses, for a machine
	// whose local config sets none.
	DefaultBase = 5100
	// DefaultStep is how many ports each job gets on main, for a machine
	// whose local config sets none.
	DefaultStep = 10

	maxPort = 65535
)

// ErrNoFreeRange means every range that fits under the highest port is held
// by an open job.
var ErrNoFreeRange = errors.New("portrange: no free port range")

// Range is the ports First through Last, both included.
type Range struct {
	First int
	Last  int
}

// Allocator hands out ranges to jobs. It is safe for concurrent use.
type Allocator struct {
	base int
	step int

	// mu guards held and taken, so two jobs asking at once never get the
	// same n.
	mu    sync.Mutex
	held  map[domain.JobID]int
	taken map[int]bool
}

// New returns an Allocator whose range n starts at base + step × n.
func New(base, step int) (*Allocator, error) {
	if base < 1 || base > maxPort {
		return nil, fmt.Errorf("port base %d: %w", base, domain.ErrInvalidValue)
	}
	// Capping step keeps base + step × n from overflowing before the loop
	// in Acquire sees it pass maxPort.
	if step < 1 || step > maxPort {
		return nil, fmt.Errorf("port step %d: %w", step, domain.ErrInvalidValue)
	}
	return &Allocator{base: base, step: step, held: map[domain.JobID]int{}, taken: map[int]bool{}}, nil
}

// Acquire returns job's range: the one it already holds, or else the free one
// with the smallest n. It fails with ErrNoFreeRange when no range that fits
// under the highest port is free.
func (a *Allocator) Acquire(job domain.JobID) (Range, error) {
	if job == "" {
		return Range{}, fmt.Errorf("job id: %w", domain.ErrMissingField)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n, ok := a.held[job]; ok {
		return a.rangeOf(n), nil
	}
	for n := 0; a.rangeOf(n).Last <= maxPort; n++ {
		if !a.taken[n] {
			a.taken[n] = true
			a.held[job] = n
			return a.rangeOf(n), nil
		}
	}
	return Range{}, fmt.Errorf("job %s: %w", job, ErrNoFreeRange)
}

// Release frees job's range for the next job that asks. A job holding none
// is left alone, so releasing twice is harmless.
func (a *Allocator) Release(job domain.JobID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n, ok := a.held[job]; ok {
		delete(a.held, job)
		delete(a.taken, n)
	}
}

func (a *Allocator) rangeOf(n int) Range {
	first := a.base + a.step*n
	return Range{First: first, Last: first + a.step - 1}
}
