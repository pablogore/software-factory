package configyaml

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// ModelPlaceholder is replaced, in an agent's args or env values, with the
// model of the role a session is for. ACP has no stable method to pick a
// model, so each agent gets it the way its own CLI takes it: a flag such as
// `--model {model}` or a variable such as `ANTHROPIC_MODEL: "{model}"`.
const ModelPlaceholder = "{model}"

// ErrUnknownAgent means a role names an agent roles.yaml does not configure.
var ErrUnknownAgent = errors.New("agent not configured")

// Agent is how one configured agent is launched.
type Agent struct {
	Command string
	Args    []string
	// Env is NAME=value pairs, sorted by name.
	Env []string
}

// Agents are the configured agents by name.
type Agents map[string]Agent

type agentsFile struct {
	Agents map[string]agentFile `yaml:"agents"`
}

type agentFile struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}

// LoadAgents parses roles.yaml (05-config.md): each agent a role can name,
// with the command that launches it. Every agent takes ModelPlaceholder in
// its args or env, so a role's model is never dropped without a word.
func LoadAgents(data []byte) (Agents, error) {
	var f agentsFile
	if err := decodeStrict(data, &f); err != nil {
		return nil, fmt.Errorf("parse roles.yaml: %w", err)
	}
	if len(f.Agents) == 0 {
		return nil, fmt.Errorf("roles.yaml: %w: agents", domain.ErrMissingField)
	}
	agents := Agents{}
	// In name order, so a file with two faults always reports the same one.
	for _, name := range slices.Sorted(maps.Keys(f.Agents)) {
		a := f.Agents[name]
		if a.Command == "" {
			return nil, fmt.Errorf("roles.yaml: agent %s: %w: command", name, domain.ErrMissingField)
		}
		var env []string
		for k, v := range a.Env {
			env = append(env, k+"="+v)
		}
		slices.Sort(env)
		ag := Agent{Command: a.Command, Args: a.Args, Env: env}
		if !slices.ContainsFunc(append(slices.Clone(ag.Args), ag.Env...), takesModel) {
			return nil, fmt.Errorf("roles.yaml: agent %s: %w: no %s in its args or env, so it would never get a role's model", name, domain.ErrInvalidValue, ModelPlaceholder)
		}
		agents[name] = ag
	}
	return agents, nil
}

func takesModel(s string) bool { return strings.Contains(s, ModelPlaceholder) }

// For is how r's sessions are launched: the agent r names, with r's model in
// place of ModelPlaceholder. The error names the role and, when it is not
// configured, the agent.
func (as Agents) For(r domain.Role) (Agent, error) {
	if r.Agent == "" {
		return Agent{}, fmt.Errorf("role %s: %w: agent", r.Name, domain.ErrMissingField)
	}
	a, ok := as[r.Agent]
	if !ok {
		return Agent{}, fmt.Errorf("role %s names agent %q: %w in roles.yaml", r.Name, r.Agent, ErrUnknownAgent)
	}
	if r.Model == "" {
		return Agent{}, fmt.Errorf("role %s: %w: model", r.Name, domain.ErrMissingField)
	}
	fill := func(s string) string { return strings.ReplaceAll(s, ModelPlaceholder, r.Model) }
	out := Agent{Command: a.Command}
	for _, s := range a.Args {
		out.Args = append(out.Args, fill(s))
	}
	for _, s := range a.Env {
		out.Env = append(out.Env, fill(s))
	}
	return out, nil
}
