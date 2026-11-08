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
	"net"
	"slices"
	"strconv"
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

// ErrNoFreePort means every port of a job's range is taken or in use.
var ErrNoFreePort = errors.New("portrange: no free port in the job's range")

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

// Free returns the first port of job's range, acquiring it if need be, that
// is not in taken and that nothing listens on: a server that was started on
// a port but does not listen yet is in taken, so two never get the same one.
func (a *Allocator) Free(job domain.JobID, taken []int) (int, error) {
	r, err := a.Acquire(job)
	if err != nil {
		return 0, err
	}
	for p := r.First; p <= r.Last; p++ {
		if slices.Contains(taken, p) {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			continue
		}
		if err := ln.Close(); err != nil {
			return 0, fmt.Errorf("port %d: %w", p, err)
		}
		return p, nil
	}
	return 0, fmt.Errorf("job %s, ports %d-%d: %w", job, r.First, r.Last, ErrNoFreePort)
}
