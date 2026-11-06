package domain

import (
	"fmt"
	"slices"
	"strconv"
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

// DefaultMaxRounds is how many rounds a review stage allows when neither the
// stage nor limits.max_rounds sets it, the value pipelines/default.yaml uses.
const DefaultMaxRounds = 3

// Limits are the pipeline's retry limits. A nil value is not set and takes
// its default; a nil HumanReworks, set or not, is unlimited.
type Limits struct {
	SpecRejections *int `json:"spec_rejections,omitempty"`
	HumanReworks   *int `json:"human_reworks,omitempty"`
	TriageRetries  *int `json:"triage_retries,omitempty"`
	MaxRounds      *int `json:"max_rounds,omitempty"`
}

// LimitKeys are the retry limits a pipeline's limits block may set and a
// person may override for one job.
var LimitKeys = []string{"spec_rejections", "human_reworks", "triage_retries", "max_rounds"}

// PipelineCap is the most a pipeline may set limit key to, in its limits block
// or, for max_rounds, in a review stage: a retry limit high enough to let a
// job loop for long takes a per-job override (04-roles.md "Validación").
func PipelineCap(key string) int {
	if key == "triage_retries" {
		return 3
	}
	return 10
}

// OverrideCap is the most a person may set limit key to for one job.
func OverrideCap(key string) int {
	if key == "triage_retries" {
		return 5
	}
	return 20
}

// Unlimited is the value that lifts human_reworks, the only limit that may
// be unlimited.
const Unlimited = "unlimited"

// ParseLimit reads one limit as a person writes it: a known key and a whole
// number of 1 or more, or Unlimited for human_reworks, as in a pipeline's
// limits block. The error names the key.
func ParseLimit(key, value string) (n int, unlimited bool, err error) {
	if !slices.Contains(LimitKeys, key) {
		return 0, false, fmt.Errorf("%w: unknown key %q", ErrInvalidValue, key)
	}
	if key == "human_reworks" && value == Unlimited {
		return 0, true, nil
	}
	n, err = strconv.Atoi(value)
	if err != nil || n < 1 {
		want := "want a whole number of 1 or more"
		if key == "human_reworks" {
			want += " or " + Unlimited
		}
		return 0, false, fmt.Errorf("%w: %s %q, %s", ErrInvalidValue, key, value, want)
	}
	return n, false, nil
}

// Validate checks that every limit that is set is at least 1 and at most its
// PipelineCap, naming the key.
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
		if v.n != nil && *v.n > PipelineCap(v.key) {
			return fmt.Errorf("limits: %w: %s %d, want at most %d", ErrInvalidValue, v.key, *v.n, PipelineCap(v.key))
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

// HumanReworks is how many reworks back to build a person may send from needs-human:
// limits.human_reworks, and false when it is not set (unlimited).
func (p Pipeline) HumanReworks() (int, bool) {
	if p.Limits.HumanReworks == nil {
		return 0, false
	}
	return *p.Limits.HumanReworks, true
}

// MaxRounds is how many rounds review stage st allows: its own max_rounds,
// else limits.max_rounds, else DefaultMaxRounds.
func (p Pipeline) MaxRounds(st Stage) int {
	switch {
	case st.MaxRounds != nil:
		return *st.MaxRounds
	case p.Limits.MaxRounds != nil:
		return *p.Limits.MaxRounds
	default:
		return DefaultMaxRounds
	}
}

// Stage is one step of a pipeline. Its roles run in parallel; a review stage
// says where to go back on CHANGES and how many rounds it allows; a nil
// MaxRounds is not set and follows Pipeline.MaxRounds.
type Stage struct {
	Name      string   `json:"name"`
	Kind      Kind     `json:"kind"`
	Roles     []string `json:"roles,omitempty"`
	OnChanges string   `json:"on_changes,omitempty"`
	MaxRounds *int     `json:"max_rounds,omitempty"`
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
// reserved, stages run build, then review, then exactly one approve, then only
// tasks, every review goes back to an earlier stage, lists roles and allows at
// least one round and at most the max_rounds cap when it sets max_rounds, every
// role a stage lists is well formed, listed once in the stage and exists, and
// every limit set is between 1 and its cap.
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
	built, reviewed, approved := false, false, false
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
		// Work must pass build, then review, then one approve; anything after
		// the approve could change approved work, so only tasks may follow.
		if err := checkOrder(st, built, reviewed, approved); err != nil {
			return err
		}
		switch st.Kind {
		case KindBuild:
			built = true
		case KindReview:
			reviewed = true
		case KindApprove:
			approved = true
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
		for i, role := range st.Roles {
			if err := ValidateRoleName(role); err != nil {
				return fmt.Errorf("stage %s: %w", st.Name, err)
			}
			// Each listed role runs once; a repeat would count one reviewer twice.
			if slices.Contains(st.Roles[:i], role) {
				return fmt.Errorf("stage %s: %w: role %q is listed twice", st.Name, ErrInvalidValue, role)
			}
			if !known[role] {
				return fmt.Errorf("stage %s: %w: role %q is not loaded", st.Name, ErrInvalidValue, role)
			}
		}
		if st.Kind == KindReview && st.MaxRounds != nil && *st.MaxRounds < 1 {
			return fmt.Errorf("stage %s: %w: max_rounds %d, want at least 1", st.Name, ErrInvalidValue, *st.MaxRounds)
		}
		if st.Kind == KindReview && st.MaxRounds != nil && *st.MaxRounds > PipelineCap("max_rounds") {
			return fmt.Errorf("stage %s: %w: max_rounds %d, want at most %d", st.Name, ErrInvalidValue, *st.MaxRounds, PipelineCap("max_rounds"))
		}
		seen[st.Name] = true
	}
	if !approved {
		return fmt.Errorf("%s: %w: approve stage", p.Label(), ErrMissingField)
	}
	return nil
}

// checkOrder names st when it breaks the stage order build, review, one
// approve, then only tasks, given the kinds seen before it.
func checkOrder(st Stage, built, reviewed, approved bool) error {
	switch {
	case approved && st.Kind == KindApprove:
		return fmt.Errorf("stage %s: %w: second approve stage, want exactly 1", st.Name, ErrInvalidValue)
	case approved && st.Kind == KindReview:
		return fmt.Errorf("stage %s: %w: review stage comes after the approve stage", st.Name, ErrInvalidValue)
	case approved && st.Kind != KindTask:
		return fmt.Errorf("stage %s: %w: only task stages may come after the approve stage", st.Name, ErrInvalidValue)
	case st.Kind == KindReview && !built:
		return fmt.Errorf("stage %s: %w: review stage comes before the build stage", st.Name, ErrInvalidValue)
	case st.Kind == KindApprove && !reviewed:
		// An approve with no review before it would sign off unreviewed work.
		return fmt.Errorf("stage %s: %w: approve stage has no review stage before it", st.Name, ErrInvalidValue)
	}
	return nil
}

// WithDefaultRoles returns p with every stage that lists no roles given the
// DefaultRole of its kind, so Decide takes a stage's results only from its own
// roles. A stage whose kind has no default is an error that names it. The
// default role needs no role file: none exists for it yet.
func (p Pipeline) WithDefaultRoles() (Pipeline, error) {
	stages := slices.Clone(p.Stages)
	for i, st := range stages {
		if len(st.Roles) > 0 {
			continue
		}
		role, ok := DefaultRole(st.Kind)
		if !ok {
			return Pipeline{}, fmt.Errorf("stage %s: %w: kind %s lists no roles and has no default role", st.Name, ErrMissingField, st.Kind)
		}
		stages[i].Roles = []string{role}
	}
	p.Stages = stages
	return p, nil
}
