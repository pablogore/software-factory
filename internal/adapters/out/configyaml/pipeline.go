// Package configyaml parses factory configuration YAML (pipelines/*.yaml) into
// domain types. Parsing lives here so domain keeps to the standard library;
// domain validates what this package hands it.
package configyaml

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/pablogore/software-factory/internal/domain"
)

type pipelineFile struct {
	Name   string      `yaml:"name"`
	Stages []yaml.Node `yaml:"stages"`
}

type stageBody struct {
	Parallel  []string `yaml:"parallel"`
	OnChanges string   `yaml:"on_changes"`
	MaxRounds int      `yaml:"max_rounds"`
}

// LoadPipeline parses a pipeline file and validates it against the roles
// loaded alongside it.
func LoadPipeline(data []byte, roles []domain.Role) (domain.Pipeline, error) {
	var f pipelineFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return domain.Pipeline{}, fmt.Errorf("parse pipeline: %w", err)
	}
	p := domain.Pipeline{Name: f.Name}
	for i := range f.Stages {
		st, err := parseStage(&f.Stages[i])
		if err != nil {
			return domain.Pipeline{}, fmt.Errorf("pipeline %s: stages[%d]: %w", f.Name, i, err)
		}
		p.Stages = append(p.Stages, st)
	}
	if err := p.Validate(roles); err != nil {
		return domain.Pipeline{}, err
	}
	return p, nil
}

// parseStage reads either a bare name (`- intake`), which is a stage of that
// kind, or a one-key map (`- review: {...}`) whose key gives the kind.
func parseStage(n *yaml.Node) (domain.Stage, error) {
	switch {
	case n.Kind == yaml.ScalarNode:
		return domain.Stage{Name: n.Value, Kind: domain.Kind(n.Value)}, nil
	case n.Kind == yaml.MappingNode && len(n.Content) == 2:
		name := n.Content[0].Value
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
