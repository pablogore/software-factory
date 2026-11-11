package configyaml

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/pablogore/software-factory/internal/domain"
)

// roleFile lists every key 04-roles.md documents for a role. Permissions
// and budget are accepted so documented files load, but nothing uses them
// yet, so they don't reach domain.Role.
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
	AlwaysRerun  yaml.Node `yaml:"always_rerun"`
	AllowedPaths []string  `yaml:"allowed_paths"`
}

type whenFile struct {
	Any []conditionFile `yaml:"any"`
}

type conditionFile struct {
	Risk        []string `yaml:"risk"`
	Surfaces    []string `yaml:"surfaces"`
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
	r.AllowedPaths = f.AllowedPaths
	r.Tools = f.Tools
	for _, c := range f.When.Any {
		var risk []domain.Risk
		for _, level := range c.Risk {
			risk = append(risk, domain.Risk(level))
		}
		r.When.Any = append(r.When.Any, domain.Condition{
			Risk:        risk,
			Surfaces:    c.Surfaces,
			Paths:       c.Paths,
			Labels:      c.Labels,
			DepsChanged: c.DepsChanged,
			DiffLines:   c.DiffLines,
		})
	}
	// Validated first so a badly formed name is the error, not a later one
	// that prints it unquoted.
	if err := r.Validate(); err != nil {
		return domain.Role{}, err
	}
	rerun, err := alwaysRerun(&f.AlwaysRerun)
	if err != nil {
		return domain.Role{}, fmt.Errorf("role %s: %w", f.Name, err)
	}
	r.AlwaysRerun = rerun
	return r, nil
}

// alwaysRerun reads always_rerun: off when the key is absent, otherwise it
// must be the YAML boolean true or false.
func alwaysRerun(n *yaml.Node) (bool, error) {
	switch n.Kind {
	case 0:
		return false, nil
	case yaml.SequenceNode:
		return false, fmt.Errorf("%w: line %d: always_rerun is a list, want true or false", domain.ErrInvalidValue, n.Line)
	case yaml.MappingNode:
		return false, fmt.Errorf("%w: line %d: always_rerun is a map, want true or false", domain.ErrInvalidValue, n.Line)
	}
	var v bool
	if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || n.Decode(&v) != nil {
		return false, fmt.Errorf("%w: line %d: always_rerun %q, want true or false", domain.ErrInvalidValue, n.Line, n.Value)
	}
	return v, nil
}

// decodeStrict decodes data into v and fails on a key v doesn't declare, so a
// typo is an error instead of a silently missing setting. Keys are checked on
// the node tree first so their error names the key, not the Go type behind it.
func decodeStrict(data []byte, v any) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	// The decoder reports an empty file as a bare EOF; a lone `---` holds only
	// a null and would load as a role or pipeline with every field blank.
	if len(doc.Content) == 0 || doc.Content[0].Tag == "!!null" {
		return fmt.Errorf("%w: file is empty", domain.ErrInvalidValue)
	}
	if err := checkKeys(doc.Content[0], reflect.TypeOf(v).Elem()); err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}

var nodeType = reflect.TypeFor[yaml.Node]()

// checkKeys walks n alongside the struct type t and fails on the first map key
// t doesn't declare, at any depth. Fields kept as yaml.Node are checked by
// their own parser.
func checkKeys(n *yaml.Node, t reflect.Type) error {
	// An alias decodes as the node it names, so that node is what gets checked.
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	switch {
	case t == nodeType:
		return nil
	case t.Kind() == reflect.Slice && n.Kind == yaml.SequenceNode:
		for _, item := range n.Content {
			if err := checkKeys(item, t.Elem()); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Map && n.Kind == yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			if err := checkKeys(n.Content[i], t.Elem()); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Struct && n.Kind == yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Tag == "!!merge" {
				if err := checkMerge(v, t); err != nil {
					return err
				}
				continue
			}
			ft := fieldType(t, k.Value)
			if ft == nil {
				return fmt.Errorf("%w: line %d: unknown key %q", domain.ErrInvalidValue, k.Line, k.Value)
			}
			if err := checkKeys(v, ft); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkMerge checks the maps a merge key (`<<: *x` or `<<: [*x, *y]`) pulls
// in against t, the struct they are merged into.
func checkMerge(v *yaml.Node, t reflect.Type) error {
	if v.Kind != yaml.SequenceNode {
		return checkKeys(v, t)
	}
	for _, item := range v.Content {
		if err := checkKeys(item, t); err != nil {
			return err
		}
	}
	return nil
}

// fieldType is the type of the field of struct type t with yaml key key, or
// nil when t declares no such key.
func fieldType(t reflect.Type, key string) reflect.Type {
	for i := range t.NumField() {
		if yamlKey(t.Field(i)) == key {
			return t.Field(i).Type
		}
	}
	return nil
}

func yamlKey(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	return name
}
