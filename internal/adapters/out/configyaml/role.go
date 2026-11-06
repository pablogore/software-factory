package configyaml

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

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
	// AlwaysRerun is a node, not a bool, so a value that isn't true or false
	// fails with an error naming the role instead of a bare decode error.
	AlwaysRerun yaml.Node `yaml:"always_rerun"`
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
	rerun, err := alwaysRerun(&f.AlwaysRerun)
	if err != nil {
		return domain.Role{}, fmt.Errorf("role %s: %w", f.Name, err)
	}
	r.AlwaysRerun = rerun
	if err := r.Validate(); err != nil {
		return domain.Role{}, err
	}
	return r, nil
}

// alwaysRerun reads always_rerun: off when the key is absent, otherwise it
// must be the YAML boolean true or false.
func alwaysRerun(n *yaml.Node) (bool, error) {
	if n.Kind == 0 {
		return false, nil
	}
	var v bool
	if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || n.Decode(&v) != nil {
		return false, fmt.Errorf("%w: line %d: always_rerun %q, want true or false", domain.ErrInvalidValue, n.Line, n.Value)
	}
	return v, nil
}

// decodeStrict decodes data into v and fails on a key v doesn't declare, so a
// typo is an error instead of a silently missing setting. Top-level keys are
// checked first so their error names the key, not the Go type behind it.
func decodeStrict(data []byte, v any) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 1 {
		if err := knownKeys(doc.Content[0], yamlKeys(v)...); err != nil {
			return err
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}

// yamlKeys lists the yaml keys of the struct v points to, so the top-level
// check can't drift from the fields decodeStrict decodes into.
func yamlKeys(v any) []string {
	t := reflect.TypeOf(v).Elem()
	keys := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		keys = append(keys, name)
	}
	return keys
}
