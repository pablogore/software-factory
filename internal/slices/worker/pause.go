package worker

import (
	"sync"
	"time"
)

// pauses are the agents that hit their usage or rate limit, each with when
// it may go on. A pause is by the agent's name, as roles.yaml names it, not
// by model or account: every role on that agent waits it out.
type pauses struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// pause holds agent until until, unless it is already held for longer.
func (p *pauses) pause(agent string, until time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.until == nil {
		p.until = map[string]time.Time{}
	}
	if until.After(p.until[agent]) {
		p.until[agent] = until
	}
}

// pausedFor is how long agent is still held at now, or 0 when it may run.
func (p *pauses) pausedFor(agent string, now time.Time) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(p.until[agent].Sub(now), 0)
}
