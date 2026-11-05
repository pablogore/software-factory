package domain

// JobID identifies a job inside factoryd.
type JobID string

// Job is one unit of work, from ticket to merge. It is internal to factoryd:
// the tracker issue and the PR are mirrors of it.
type Job struct {
	ID            JobID          `json:"id"`
	Title         string         `json:"title"`
	Pipeline      string         `json:"pipeline"`
	Stage         string         `json:"stage"`
	Round         int            `json:"round"`
	Origin        Origin         `json:"origin"`
	Subscriptions []Subscription `json:"subscriptions,omitempty"`
}
