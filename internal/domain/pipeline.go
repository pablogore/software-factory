package domain

import (
	"fmt"
	"slices"
)

// ReservedStageNames are the stages a job is in outside its pipeline, plus its
// terminal states. A pipeline stage with one of these names would be mistaken
// for them.
var ReservedStageNames = []string{"stopped", StageMerge, StageNeedsHuman, StageNeedsInfo, StageDone, "rejected"}

// Pipeline is the flow a job follows: an ordered list of stages (04-roles.md).
// Roles are the roles its stages list, as loaded; decide reads a reviewer's
// mode from them, and a role missing here counts as blocking.
type Pipeline struct {
	Name   string  `json:"name"`
	Stages []Stage `json:"stages"`
	Roles  []Role  `json:"roles,omitempty"`
	Limits Limits  `json:"limits,omitzero"`
}

// DefaultSpecRejections is how many specgate rejections a job takes before a
// person must look at it when the pipeline doesn't set spec_rejections, as in
// the current factory.
const DefaultSpecRejections = 3

// Limits are the pipeline's retry limits. A nil value is not set and takes
// its default; a nil HumanReworks, set or not, is unlimited.
type Limits struct {
	SpecRejections *int `json:"spec_rejections,omitempty"`
	HumanReworks   *int `json:"human_reworks,omitempty"`
	TriageRetries  *int `json:"triage_retries,omitempty"`
	MaxRounds      *int `json:"max_rounds,omitempty"`
}

// Validate checks that every limit that is set is at least 1, naming the key.
func (l Limits) Validate() error {
	for _, v := range []struct {
		key string
		n   *int
	}{
		{"spec_rejections", l.SpecRejections},
		{"human_reworks", l.HumanReworks},
		{"triage_retries", l.TriageRetries},
		{"max_rounds", l.MaxRounds},
	} {
		if v.n != nil && *v.n < 1 {
			return fmt.Errorf("limits: %w: %s %d, want at least 1", ErrInvalidValue, v.key, *v.n)
		}
	}
	return nil
}

// SpecRejections is how many specgate rejections send a job to a person:
// limits.spec_rejections, or DefaultSpecRejections when it is not set.
func (p Pipeline) SpecRejections() int {
	if p.Limits.SpecRejections == nil {
		return DefaultSpecRejections
	}
	return *p.Limits.SpecRejections
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

// Label names the pipeline in errors: "pipeline <name>", or just "pipeline"
// when it has no name, so the message never shows a blank name.
func (p Pipeline) Label() string {
	if p.Name == "" {
		return "pipeline"
	}
	return "pipeline " + p.Name
}

// Validate checks the flow rules of 04-roles.md against the roles loaded with
// the pipeline: exactly one build stage, stage names are unique and not
// reserved, every review goes back to an earlier stage, lists roles and allows
// at least one round, every role a stage lists exists, and every limit set is
// at least 1.
func (p Pipeline) Validate(roles []Role) error {
	if err := p.Limits.Validate(); err != nil {
		return fmt.Errorf("%s: %w", p.Label(), err)
	}
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
		return fmt.Errorf("%s: %w: %d build stages, want exactly 1", p.Label(), ErrInvalidValue, builds)
	}
	seen := make(map[string]bool, len(p.Stages))
	for _, st := range p.Stages {
		// Checked before the kind: a YAML stage's kind is its name, and the
		// reserved name is the real fault.
		if slices.Contains(ReservedStageNames, st.Name) {
			return fmt.Errorf("stage %s: %w: stage name is reserved", st.Name, ErrInvalidValue)
		}
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
