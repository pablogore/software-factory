package domain

import "fmt"

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

// Validate checks the flow rules of 04-roles.md against the roles loaded with
// the pipeline: exactly one build stage, stage names are unique, every review
// goes back to an earlier stage, lists roles and allows at least one round, and
// every role a stage lists exists.
func (p Pipeline) Validate(roles []Role) error {
	known := make(map[string]bool, len(roles))
	for _, r := range roles {
		known[r.Name] = true
	}
	builds := 0
	for _, st := range p.Stages {
		if st.Kind == KindBuild {
			builds++
		}
	}
	if builds != 1 {
		return fmt.Errorf("pipeline %s: %w: %d build stages, want exactly 1", p.Name, ErrInvalidValue, builds)
	}
	seen := make(map[string]bool, len(p.Stages))
	for _, st := range p.Stages {
		if err := st.Kind.Validate(); err != nil {
			return fmt.Errorf("stage %s: %w", st.Name, err)
		}
		// A repeated name makes on_changes ambiguous.
		if seen[st.Name] {
			return fmt.Errorf("stage %s: %w: stage name is repeated", st.Name, ErrInvalidValue)
		}
		if st.Kind == KindReview {
			// on_changes must point back, so the flow stays a DAG plus its review loops.
			if st.OnChanges == "" {
				return fmt.Errorf("stage %s: %w: on_changes", st.Name, ErrMissingField)
			}
			if !seen[st.OnChanges] {
				return fmt.Errorf("stage %s: %w: on_changes %q is not an earlier stage", st.Name, ErrInvalidValue, st.OnChanges)
			}
			if len(st.Roles) == 0 {
				return fmt.Errorf("stage %s: %w: parallel", st.Name, ErrMissingField)
			}
		}
		for _, role := range st.Roles {
			if !known[role] {
				return fmt.Errorf("stage %s: %w: role %q is not loaded", st.Name, ErrInvalidValue, role)
			}
		}
		if st.Kind == KindReview && st.MaxRounds < 1 {
			return fmt.Errorf("stage %s: %w: max_rounds %d, want at least 1", st.Name, ErrInvalidValue, st.MaxRounds)
		}
		seen[st.Name] = true
	}
	return nil
}
