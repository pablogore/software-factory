package configyaml

import (
	"bytes"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/pablogore/software-factory/internal/domain"
)

// roleFile lists every key 04-roles.md documents for a role. Tools,
// permissions and budget are accepted so documented files load, but nothing
// uses them yet, so they don't reach domain.Role.
type roleFile struct {
	Name        string     `yaml:"name"`
	Kind        string     `yaml:"kind"`
	Description string     `yaml:"description"`
	Prompt      string     `yaml:"prompt"`
	Agent       string     `yaml:"agent"`
	Model       string     `yaml:"model"`
	When        whenFile   `yaml:"when"`
	Mode        string     `yaml:"mode"`
	Tools       []string   `yaml:"tools"`
	Permissions string     `yaml:"permissions"`
	Budget      budgetFile `yaml:"budget"`
}

type whenFile struct {
	Any []conditionFile `yaml:"any"`
}

type conditionFile struct {
	Paths       []string `yaml:"paths"`
	Labels      []string `yaml:"labels"`
	DepsChanged bool     `yaml:"deps_changed"`
	DiffLines   string   `yaml:"diff_lines"`
}

type budgetFile struct {
	Attempts int     `yaml:"attempts"`
	Minutes  int     `yaml:"minutes"`
	USD      float64 `yaml:"usd"`
}

// LoadRole parses a role file (roles/<name>.yaml) and validates it.
func LoadRole(data []byte) (domain.Role, error) {
	var f roleFile
	if err := decodeStrict(data, &f); err != nil {
		return domain.Role{}, fmt.Errorf("parse role: %w", err)
	}
	r := domain.Role{
		Name:        f.Name,
		Kind:        domain.Kind(f.Kind),
		Description: f.Description,
		Prompt:      f.Prompt,
		Agent:       f.Agent,
		Model:       f.Model,
		Mode:        domain.Mode(f.Mode),
	}
	for _, c := range f.When.Any {
		r.When.Any = append(r.When.Any, domain.Condition{
			Paths:       c.Paths,
			Labels:      c.Labels,
			DepsChanged: c.DepsChanged,
			DiffLines:   c.DiffLines,
		})
	}
	if err := r.Validate(); err != nil {
		return domain.Role{}, err
	}
	return r, nil
}

// decodeStrict decodes data into v and fails on a key v doesn't declare, so a
// typo is an error instead of a silently missing setting.
func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}
