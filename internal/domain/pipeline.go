package domain

// Pipeline is the flow a job follows: an ordered list of stages (04-roles.md).
type Pipeline struct {
	Name   string  `json:"name"`
	Stages []Stage `json:"stages"`
}

// Stage is one step of a pipeline. Its roles run in parallel; a review stage
// says where to go back on CHANGES and how many rounds it allows.
type Stage struct {
	Name      string   `json:"name"`
	Kind      Kind     `json:"kind"`
	Roles     []string `json:"roles,omitempty"`
	OnChanges string   `json:"on_changes,omitempty"`
	MaxRounds int      `json:"max_rounds,omitempty"`
}
