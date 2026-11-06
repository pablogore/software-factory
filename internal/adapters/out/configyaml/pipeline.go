// Package configyaml parses factory configuration YAML (pipelines/*.yaml) into
// domain types. Parsing lives here so domain keeps to the standard library;
// domain validates what this package hands it.
package configyaml

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/pablogore/software-factory/internal/domain"
)

type pipelineFile struct {
	Name   string      `yaml:"name"`
	Stages []yaml.Node `yaml:"stages"`
	Limits yaml.Node   `yaml:"limits"`
}

type stageBody struct {
	Parallel  []string `yaml:"parallel"`
	OnChanges string   `yaml:"on_changes"`
	MaxRounds *int     `yaml:"max_rounds"`
}

// LoadPipeline parses a pipeline file and validates it against the roles
// loaded alongside it. The pipeline keeps the roles its stages name, so Decide
// reads each reviewer's mode from its role file.
func LoadPipeline(data []byte, roles []domain.Role) (domain.Pipeline, error) {
	var f pipelineFile
	if err := decodeStrict(data, &f); err != nil {
		return domain.Pipeline{}, fmt.Errorf("parse pipeline: %w", err)
	}
	p := domain.Pipeline{Name: f.Name}
	for i := range f.Stages {
		st, err := parseStage(&f.Stages[i])
		if err != nil {
			return domain.Pipeline{}, fmt.Errorf("%s: stages[%d]: %w", p.Label(), i, err)
		}
		p.Stages = append(p.Stages, st)
	}
	limits, err := parseLimits(&f.Limits)
	if err != nil {
		return domain.Pipeline{}, fmt.Errorf("%s: limits: %w", p.Label(), err)
	}
	p.Limits = limits
	if err := p.Validate(roles); err != nil {
		return domain.Pipeline{}, err
	}
	named, err := stageRoles(p.Stages, roles)
	if err != nil {
		return domain.Pipeline{}, err
	}
	p.Roles = named
	return p, nil
}

// stageRoles returns, in the order the stages first name them, the roles the
// stages list. Decide treats a role without a mode as blocking, so a mode
// missing from a role file must fail here instead of blocking silently.
func stageRoles(stages []domain.Stage, roles []domain.Role) ([]domain.Role, error) {
	var out []domain.Role
	for _, st := range stages {
		for _, name := range st.Roles {
			if slices.ContainsFunc(out, func(r domain.Role) bool { return r.Name == name }) {
				continue
			}
			// Pipeline.Validate has checked that every listed role is loaded.
			i := slices.IndexFunc(roles, func(r domain.Role) bool { return r.Name == name })
			if err := roles[i].Mode.Validate(); err != nil {
				return nil, fmt.Errorf("role %s: %w", name, err)
			}
			out = append(out, roles[i])
		}
	}
	return out, nil
}

// parseStage reads either a bare name (`- intake`), which is a stage of that
// kind, or a one-key map (`- review: {...}`) whose key gives the kind.
func parseStage(n *yaml.Node) (domain.Stage, error) {
	switch {
	case n.Kind == yaml.ScalarNode:
		return domain.Stage{Name: n.Value, Kind: domain.Kind(n.Value)}, nil
	case n.Kind == yaml.MappingNode && len(n.Content) == 2:
		name := n.Content[0].Value
		if err := knownKeys(n.Content[1], "parallel", "on_changes", "max_rounds"); err != nil {
			return domain.Stage{}, fmt.Errorf("stage %s: %w", name, err)
		}
		var b stageBody
		if err := n.Content[1].Decode(&b); err != nil {
			return domain.Stage{}, fmt.Errorf("stage %s: %w", name, err)
		}
		return domain.Stage{
			Name:      name,
			Kind:      domain.Kind(name),
			Roles:     b.Parallel,
			OnChanges: b.OnChanges,
			MaxRounds: b.MaxRounds,
		}, nil
	default:
		return domain.Stage{}, fmt.Errorf("%w: line %d: want a stage name or a one-key map", domain.ErrInvalidValue, n.Line)
	}
}

// limitKeys are the keys a limits block may set.
var limitKeys = []string{"spec_rejections", "human_reworks", "triage_retries", "max_rounds"}

// parseLimits reads the limits block. A value must be a YAML integer, and
// human_reworks may also be "unlimited"; Limits.Validate checks the range.
// Each value is decoded on its own so a bad one is named by its key.
func parseLimits(n *yaml.Node) (domain.Limits, error) {
	var l domain.Limits
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Kind == 0 || n.Tag == "!!null" {
		return l, nil
	}
	if n.Kind != yaml.MappingNode {
		return l, fmt.Errorf("%w: line %d: want a map of limits", domain.ErrInvalidValue, n.Line)
	}
	if err := knownKeys(n, limitKeys...); err != nil {
		return l, err
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, v := n.Content[i].Value, n.Content[i+1]
		if v.Kind == yaml.AliasNode {
			v = v.Alias
		}
		if key == "human_reworks" && v.Kind == yaml.ScalarNode && v.Tag == "!!str" && v.Value == "unlimited" {
			continue
		}
		var x int
		if v.Kind != yaml.ScalarNode || v.Tag != "!!int" || v.Decode(&x) != nil {
			want := "want a whole number of 1 or more"
			if key == "human_reworks" {
				want += " or unlimited"
			}
			return l, fmt.Errorf("%w: line %d: %s %q, %s", domain.ErrInvalidValue, v.Line, key, v.Value, want)
		}
		switch key {
		case "spec_rejections":
			l.SpecRejections = &x
		case "human_reworks":
			l.HumanReworks = &x
		case "triage_retries":
			l.TriageRetries = &x
		case "max_rounds":
			l.MaxRounds = &x
		}
	}
	return l, nil
}

// knownKeys fails on a map key not in keys. Node.Decode can't decode strictly,
// so a stage body is checked here before it is decoded.
func knownKeys(n *yaml.Node, keys ...string) error {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i]
		if !slices.Contains(keys, k.Value) {
			return fmt.Errorf("%w: line %d: unknown key %q", domain.ErrInvalidValue, k.Line, k.Value)
		}
	}
	return nil
}
