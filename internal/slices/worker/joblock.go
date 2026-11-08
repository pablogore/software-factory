package worker

import (
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
)

// jobLocks is one mutex per job, kept only while some call holds or waits
// for it, so the map does not grow with every job the server has seen.
type jobLocks struct {
	mu    sync.Mutex
	locks map[domain.JobID]*jobLock
}

type jobLock struct {
	mu sync.Mutex
	// users counts the calls holding or waiting for mu.
	users int
}

// lock waits for job's mutex and returns the func that releases it.
func (l *jobLocks) lock(job domain.JobID) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[domain.JobID]*jobLock{}
	}
	j := l.locks[job]
	if j == nil {
		j = &jobLock{}
		l.locks[job] = j
	}
	j.users++
	l.mu.Unlock()
	j.mu.Lock()
	return func() {
		j.mu.Unlock()
		l.mu.Lock()
		j.users--
		if j.users == 0 {
			delete(l.locks, job)
		}
		l.mu.Unlock()
	}
}
