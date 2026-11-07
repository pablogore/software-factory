package memstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pablogore/software-factory/internal/adapters/out/eventjson"
	"github.com/pablogore/software-factory/internal/domain"
)

// Plant saves job and evs as they are, past Decide and Validate, so a test
// can hold what no Apply would save, such as a job breaking an invariant. It
// lives in a test file so no production code can skip the store's checks. It
// keeps the job's version, or starts it at 1.
func (s *Store) Plant(_ context.Context, job domain.Job, evs ...domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("memstore: encode job %q: %w", job.ID, err)
	}
	envs := make([]eventjson.Envelope, 0, len(evs))
	for _, ev := range evs {
		env, err := eventjson.Encode(ev)
		if err != nil {
			return fmt.Errorf("memstore: job %q: %w", job.ID, err)
		}
		envs = append(envs, env)
	}
	if _, ok := s.versions[job.ID]; !ok {
		s.versions[job.ID] = 1
	}
	s.jobs[job.ID] = data
	s.events[job.ID] = append(s.events[job.ID], envs...)
	return nil
}
