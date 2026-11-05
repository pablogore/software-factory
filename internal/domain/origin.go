package domain

// Origin is where a ticket was born: provider, thread and author
// (03-integraciones.md, "Job y origen").
type Origin struct {
	Provider string `json:"provider"`
	Thread   string `json:"thread"`
	Author   string `json:"author"`
}

// ThreadKind says what a subscribed thread is.
type ThreadKind string

// Thread kinds a job can listen to.
const (
	ThreadChat  ThreadKind = "chat"
	ThreadIssue ThreadKind = "issue"
	ThreadPR    ThreadKind = "pr"
)

// Subscription is a thread a job listens to: a message from an allowed person
// in any of them reaches the job as feedback.
type Subscription struct {
	Provider string     `json:"provider"`
	Thread   string     `json:"thread"`
	Kind     ThreadKind `json:"kind"`
}
